package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

type chainClient interface {
	ChainID(context.Context) (*big.Int, error)
	HeaderByNumber(context.Context, *big.Int) (*types.Header, error)
	CodeAt(context.Context, common.Address, *big.Int) ([]byte, error)
	CallContract(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error)
	EstimateGas(context.Context, ethereum.CallMsg) (uint64, error)
	SuggestGasTipCap(context.Context) (*big.Int, error)
	PendingNonceAt(context.Context, common.Address) (uint64, error)
	BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error)
	SendTransaction(context.Context, *types.Transaction) error
	TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error)
}

type signerJournal interface {
	Ready(context.Context) bool
	Existing(context.Context, string, string) (*Submission, error)
	Replacement(context.Context, string) (*replacementRecord, error)
	BeginNonce(context.Context, *big.Int, common.Address, uint64) (nonceReservation, error)
	InsertReplacement(context.Context, signedRecord, *replacementRecord) error
	SetSubmitted(context.Context, string) error
	SetAmbiguous(context.Context, string, string) error
	Pending(context.Context, int) ([]TransactionRecord, error)
	SetReceipt(context.Context, string, string, uint64, common.Hash, string) error
	SetSuperseded(context.Context, string, string, uint64) error
	SetFinality(context.Context, string, string) error
	DeferReconcile(context.Context, string) error
	Quarantine(context.Context, string, string) error
	ClaimAuthNonce(context.Context, string, time.Time) error
}

type Writer struct {
	config   Config
	client   chainClient
	verifier chainClient
	signer   *KMSSigner
	journal  signerJournal
	ready    atomic.Bool
	submit   sync.Mutex
}

func newWriter(
	config Config,
	client chainClient,
	verifier chainClient,
	signer *KMSSigner,
	journal signerJournal,
) *Writer {
	return &Writer{
		config:   config,
		client:   client,
		verifier: verifier,
		signer:   signer,
		journal:  journal,
	}
}

func (writer *Writer) Verify(ctx context.Context) (VerificationEvidence, error) {
	if writer.signer.Address() != writer.config.SignerAddress {
		return VerificationEvidence{}, errors.New("KMS key does not match configured signer")
	}
	primary, err := writer.verifyClient(ctx, writer.client)
	if err != nil {
		return VerificationEvidence{}, fmt.Errorf("primary verification: %w", err)
	}
	secondary, err := writer.verifyClient(ctx, writer.verifier)
	if err != nil {
		return VerificationEvidence{}, fmt.Errorf("secondary verification: %w", err)
	}
	if !writer.journal.Ready(ctx) {
		return VerificationEvidence{}, errors.New("signer journal is not ready")
	}
	return VerificationEvidence{
		PrimaryBlock:   primary.Number.Uint64(),
		PrimaryHash:    primary.Hash(),
		SecondaryBlock: secondary.Number.Uint64(),
		SecondaryHash:  secondary.Hash(),
	}, nil
}

func (writer *Writer) verifyClient(ctx context.Context, client chainClient) (*types.Header, error) {
	chainID, err := client.ChainID(ctx)
	if err != nil || chainID.Cmp(writer.config.ChainID) != 0 {
		return nil, errors.New("RPC chain ID mismatch")
	}
	head, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, errors.New("read verification block")
	}
	for _, contract := range []struct {
		name     string
		address  common.Address
		codeHash common.Hash
	}{
		{"vault", writer.config.VaultAddress, writer.config.VaultCodeHash},
		{"risk manager", writer.config.RiskManagerAddress, writer.config.RiskManagerCodeHash},
		{"spot adapter", writer.config.SpotAdapterAddress, writer.config.SpotAdapterCodeHash},
	} {
		code, err := client.CodeAt(ctx, contract.address, head.Number)
		if err != nil || len(code) == 0 || crypto.Keccak256Hash(code) != contract.codeHash {
			return nil, fmt.Errorf("%s runtime code mismatch", contract.name)
		}
	}
	vaultChecks := []struct {
		method   string
		expected common.Address
	}{
		{"agent", writer.config.SignerAddress},
		{"riskManager", writer.config.RiskManagerAddress},
		{"spotAdapter", writer.config.SpotAdapterAddress},
		{"admin", writer.config.TimelockAddress},
		{"recoveryRecipient", writer.config.RecoveryAddress},
	}
	for _, check := range vaultChecks {
		value, err := writer.readAddress(ctx, client, writer.config.VaultAddress, check.method, head.Number)
		if err != nil || value != check.expected {
			return nil, fmt.Errorf("vault %s mismatch", check.method)
		}
	}
	settlementAsset, err := writer.readAddress(ctx, client, writer.config.VaultAddress, "settlementAsset", head.Number)
	if err != nil {
		return nil, errors.New("read vault settlement asset")
	}
	contractChecks := []struct {
		name     string
		address  common.Address
		method   string
		expected common.Address
	}{
		{"risk manager executor", writer.config.RiskManagerAddress, "executor", writer.config.VaultAddress},
		{"risk manager admin", writer.config.RiskManagerAddress, "admin", writer.config.TimelockAddress},
		{"risk manager guardian", writer.config.RiskManagerAddress, "guardian", writer.config.GuardianAddress},
		{"risk manager settlement asset", writer.config.RiskManagerAddress, "settlementAsset", settlementAsset},
		{"spot adapter vault", writer.config.SpotAdapterAddress, "vault", writer.config.VaultAddress},
		{"spot adapter admin", writer.config.SpotAdapterAddress, "admin", writer.config.TimelockAddress},
		{"spot adapter settlement asset", writer.config.SpotAdapterAddress, "settlementAsset", settlementAsset},
	}
	for _, check := range contractChecks {
		value, err := writer.readAddress(ctx, client, check.address, check.method, head.Number)
		if err != nil || value != check.expected {
			return nil, fmt.Errorf("%s mismatch", check.name)
		}
	}
	return head, nil
}

func (writer *Writer) Ready() bool {
	return writer.ready.Load()
}

func (writer *Writer) Submit(ctx context.Context, request ExecuteRequest) (Submission, error) {
	writer.submit.Lock()
	defer writer.submit.Unlock()
	intent, payload, digest, err := request.validate()
	if err != nil {
		return Submission{}, err
	}
	if existing, err := writer.journal.Existing(ctx, request.RequestID, digest); err != nil {
		return Submission{}, err
	} else if existing != nil {
		return *existing, nil
	}
	if !writer.Ready() {
		return Submission{}, errWriterNotReady
	}
	evidence, err := writer.Verify(ctx)
	if err != nil {
		writer.ready.Store(false)
		return Submission{}, err
	}
	var replacement *replacementRecord
	if request.ReplacesRequestID != "" {
		replacement, err = writer.journal.Replacement(ctx, request.ReplacesRequestID)
		if err != nil {
			return Submission{}, err
		}
		var original ExecuteRequest
		if err := json.Unmarshal(replacement.Payload, &original); err != nil {
			return Submission{}, errors.New("decode replacement target")
		}
		if original.Intent != request.Intent {
			return Submission{}, errors.New("replacement cannot change the spot intent")
		}
		if replacement.Depth >= writer.config.MaxReplacementCount {
			return Submission{}, errors.New("replacement count exceeds configured limit")
		}
		if time.Since(replacement.FamilyCreatedAt) > writer.config.MaxReplacementAge {
			return Submission{}, errors.New("replacement family is too old")
		}
	}
	data, err := packExecuteSpot(intent)
	if err != nil {
		return Submission{}, errors.New("encode executeSpot")
	}
	message := ethereum.CallMsg{
		From: writer.signer.Address(),
		To:   &writer.config.VaultAddress,
		Data: data,
	}
	if _, err := writer.client.CallContract(ctx, message, nil); err != nil {
		return Submission{}, fmt.Errorf("executeSpot simulation reverted: %w", err)
	}
	estimatedGas, err := writer.client.EstimateGas(ctx, message)
	if err != nil {
		return Submission{}, errors.New("estimate executeSpot gas")
	}
	gasLimit := estimatedGas + estimatedGas/5
	if gasLimit < estimatedGas || gasLimit > writer.config.MaxGasLimit {
		return Submission{}, errors.New("gas estimate exceeds configured limit")
	}
	tip, err := writer.client.SuggestGasTipCap(ctx)
	if err != nil || tip.Sign() <= 0 {
		return Submission{}, errors.New("read priority fee")
	}
	header, err := writer.client.HeaderByNumber(ctx, nil)
	if err != nil || header.BaseFee == nil {
		return Submission{}, errors.New("read base fee")
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(header.BaseFee, big.NewInt(2)), tip)
	var nonce uint64
	var reservation nonceReservation
	if replacement != nil {
		nonce = replacement.Nonce
		tip = maximum(tip, bumped(replacement.MaxPriority))
		feeCap = maximum(feeCap, bumped(replacement.MaxFee))
		gasLimit = max(gasLimit, replacement.GasLimit)
		if gasLimit > writer.config.MaxGasLimit {
			return Submission{}, errors.New("replacement gas limit exceeds configured limit")
		}
	} else {
		observed, err := writer.client.PendingNonceAt(ctx, writer.signer.Address())
		if err != nil {
			return Submission{}, errors.New("read pending nonce")
		}
		reservation, err = writer.journal.BeginNonce(ctx, writer.config.ChainID, writer.signer.Address(), observed)
		if err != nil {
			return Submission{}, err
		}
		nonce = reservation.Nonce()
		defer reservation.Rollback(context.WithoutCancel(ctx))
	}
	if tip.Cmp(writer.config.MaxPriorityFee) > 0 {
		return Submission{}, errors.New("priority fee exceeds configured limit")
	}
	if feeCap.Cmp(writer.config.MaxFeePerGas) > 0 {
		return Submission{}, errors.New("fee cap exceeds configured limit")
	}
	maximumCost := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), feeCap)
	if maximumCost.Cmp(writer.config.MaxTransactionCost) > 0 {
		return Submission{}, errors.New("transaction cost exceeds configured limit")
	}
	balance, err := writer.client.BalanceAt(ctx, writer.signer.Address(), nil)
	if err != nil {
		return Submission{}, errors.New("read signer gas balance")
	}
	requiredBalance := new(big.Int).Add(maximumCost, writer.config.MinimumGasReserve)
	if balance.Cmp(requiredBalance) < 0 {
		return Submission{}, errors.New("signer gas reserve would be breached")
	}
	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID:   writer.config.ChainID,
		Nonce:     nonce,
		GasTipCap: tip,
		GasFeeCap: feeCap,
		Gas:       gasLimit,
		To:        &writer.config.VaultAddress,
		Value:     big.NewInt(0),
		Data:      data,
	})
	transactionSigner := types.LatestSignerForChainID(writer.config.ChainID)
	signature, err := writer.signer.SignDigest(ctx, transactionSigner.Hash(unsigned).Bytes())
	if err != nil {
		return Submission{}, err
	}
	signed, err := unsigned.WithSignature(transactionSigner, signature)
	if err != nil {
		return Submission{}, errors.New("attach KMS signature")
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		return Submission{}, errors.New("encode signed transaction")
	}
	submission := Submission{
		RequestID: request.RequestID,
		IntentID:  strings.ToLower(common.BytesToHash(intent.ID[:]).Hex()),
		TxHash:    strings.ToLower(signed.Hash().Hex()),
		Nonce:     nonce,
		Status:    SubmissionSigned,
	}
	record := signedRecord{
		Submission:     submission,
		PayloadSHA256:  digest,
		Payload:        payload,
		SignedTx:       raw,
		MaxFee:         feeCap,
		MaxPriorityFee: tip,
		GasLimit:       gasLimit,
		Evidence:       evidence,
	}
	if replacement == nil {
		err = reservation.Commit(ctx, record)
	} else {
		err = writer.journal.InsertReplacement(ctx, record, replacement)
	}
	if err != nil {
		writer.ready.Store(false)
		return Submission{}, err
	}
	if err := writer.client.SendTransaction(ctx, signed); err != nil && !isKnownTransaction(err) {
		submission.Status = SubmissionAmbiguous
		updateContext, cancel := writer.journalUpdateContext(ctx)
		updateErr := writer.journal.SetAmbiguous(updateContext, request.RequestID, boundedError(err))
		cancel()
		if updateErr != nil {
			submission.Status = SubmissionSigned
			err = errors.Join(err, errors.New("record ambiguous transaction"), updateErr)
		}
		writer.ready.Store(false)
		return submission, &journaledSubmissionError{submission: submission, cause: err}
	}
	updateContext, cancel := writer.journalUpdateContext(ctx)
	err = writer.journal.SetSubmitted(updateContext, request.RequestID)
	cancel()
	if err != nil {
		writer.ready.Store(false)
		return submission, &journaledSubmissionError{submission: submission, cause: err}
	}
	submission.Status = SubmissionSubmitted
	return submission, nil
}

func (writer *Writer) journalUpdateContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := writer.config.RequestTimeout
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func (writer *Writer) Reconcile(ctx context.Context) error {
	writer.submit.Lock()
	defer writer.submit.Unlock()
	return writer.reconcileLocked(ctx)
}

func (writer *Writer) Recover(ctx context.Context) error {
	writer.submit.Lock()
	defer writer.submit.Unlock()
	writer.ready.Store(false)
	if err := writer.reconcileLocked(ctx); err != nil {
		return err
	}
	writer.ready.Store(true)
	return nil
}

func (writer *Writer) reconcileLocked(ctx context.Context) error {
	if _, err := writer.Verify(ctx); err != nil {
		return err
	}
	records, err := writer.journal.Pending(ctx, 100)
	if err != nil {
		return err
	}
	safeHead, err := writer.finalityHead(ctx, gethrpc.SafeBlockNumber)
	if err != nil {
		return fmt.Errorf("read safe head: %w", err)
	}
	finalizedHead, err := writer.finalityHead(ctx, gethrpc.FinalizedBlockNumber)
	if err != nil {
		return fmt.Errorf("read finalized head: %w", err)
	}
	if finalizedHead.Cmp(safeHead) > 0 {
		return errors.New("finalized head is ahead of safe head")
	}
	for _, record := range records {
		transaction, err := writer.validateRecord(record)
		if err != nil {
			_ = writer.journal.Quarantine(ctx, record.RequestID, boundedError(err))
			return fmt.Errorf("quarantine invalid journal record %s", record.RequestID)
		}
		receipt, err := writer.client.TransactionReceipt(ctx, record.TxHash)
		if errors.Is(err, ethereum.NotFound) {
			if record.Status == "soft_confirmed" || record.Status == "l1_posted" {
				if updateErr := writer.journal.SetAmbiguous(ctx, record.RequestID, "canonical receipt disappeared"); updateErr != nil {
					return updateErr
				}
			}
			if record.Status != "replaced" {
				if sendErr := writer.client.SendTransaction(ctx, transaction); sendErr == nil || isKnownTransaction(sendErr) {
					if updateErr := writer.journal.SetSubmitted(ctx, record.RequestID); updateErr != nil {
						return updateErr
					}
				} else if !isNonceConsumed(sendErr) {
					return errors.New("re-broadcast journaled transaction")
				}
			}
			if err := writer.journal.DeferReconcile(ctx, record.RequestID); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return errors.New("read transaction receipt")
		}
		canonical, err := writer.client.HeaderByNumber(ctx, receipt.BlockNumber)
		if err != nil || canonical.Hash() != receipt.BlockHash {
			return errors.New("receipt block is not canonical")
		}
		secondaryCanonical, err := writer.verifier.HeaderByNumber(ctx, receipt.BlockNumber)
		if err != nil || secondaryCanonical.Hash() != receipt.BlockHash {
			return errors.New("secondary RPC disagrees with receipt block")
		}
		blockNumber := receipt.BlockNumber.Uint64()
		if receipt.Status != types.ReceiptStatusSuccessful {
			if err := writer.journal.SetReceipt(ctx, record.RequestID, "reverted", blockNumber, receipt.BlockHash, "transaction reverted"); err != nil {
				return err
			}
			if err := writer.journal.SetSuperseded(ctx, record.RequestID, record.IntentID, record.Nonce); err != nil {
				return err
			}
			continue
		}
		if err := writer.journal.SetReceipt(ctx, record.RequestID, "soft_confirmed", blockNumber, receipt.BlockHash, ""); err != nil {
			return err
		}
		if err := writer.journal.SetSuperseded(ctx, record.RequestID, record.IntentID, record.Nonce); err != nil {
			return err
		}
		if safeHead.Cmp(receipt.BlockNumber) >= 0 {
			if err := writer.journal.SetFinality(ctx, record.RequestID, "l1_posted"); err != nil {
				return err
			}
		}
		if finalizedHead.Cmp(receipt.BlockNumber) >= 0 {
			if err := writer.journal.SetFinality(ctx, record.RequestID, "ethereum_final"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (writer *Writer) validateRecord(record TransactionRecord) (*types.Transaction, error) {
	var transaction types.Transaction
	if err := transaction.UnmarshalBinary(record.SignedTx); err != nil {
		return nil, errors.New("decode journaled transaction")
	}
	if transaction.Hash() != record.TxHash || transaction.ChainId().Cmp(writer.config.ChainID) != 0 {
		return nil, errors.New("journaled transaction identity mismatch")
	}
	if transaction.Type() != types.DynamicFeeTxType || transaction.Gas() > writer.config.MaxGasLimit ||
		transaction.GasTipCap().Cmp(writer.config.MaxPriorityFee) > 0 ||
		transaction.GasFeeCap().Cmp(writer.config.MaxFeePerGas) > 0 {
		return nil, errors.New("journaled transaction exceeds fee policy")
	}
	maximumCost := new(big.Int).Mul(new(big.Int).SetUint64(transaction.Gas()), transaction.GasFeeCap())
	if maximumCost.Cmp(writer.config.MaxTransactionCost) > 0 {
		return nil, errors.New("journaled transaction exceeds cost policy")
	}
	if transaction.To() == nil || *transaction.To() != writer.config.VaultAddress || transaction.Value().Sign() != 0 {
		return nil, errors.New("journaled transaction destination mismatch")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(writer.config.ChainID), &transaction)
	if err != nil || sender != writer.config.SignerAddress {
		return nil, errors.New("journaled transaction signer mismatch")
	}
	var request ExecuteRequest
	if err := json.Unmarshal(record.Payload, &request); err != nil {
		return nil, errors.New("decode journaled payload")
	}
	intent, _, digest, err := request.validate()
	if err != nil || request.RequestID != record.RequestID {
		return nil, errors.New("journaled payload mismatch")
	}
	if digest != record.PayloadSHA256 {
		return nil, errors.New("journaled payload digest mismatch")
	}
	expected, err := packExecuteSpot(intent)
	if err != nil || !bytes.Equal(expected, transaction.Data()) {
		return nil, errors.New("journaled calldata mismatch")
	}
	if strings.ToLower(common.BytesToHash(intent.ID[:]).Hex()) != record.IntentID {
		return nil, errors.New("journaled intent mismatch")
	}
	return &transaction, nil
}

func (writer *Writer) readAddress(ctx context.Context, client chainClient, target common.Address, method string, blockNumber *big.Int) (common.Address, error) {
	input, err := vaultABI.Pack(method)
	if err != nil {
		return common.Address{}, err
	}
	output, err := client.CallContract(ctx, ethereum.CallMsg{To: &target, Data: input}, blockNumber)
	if err != nil {
		return common.Address{}, err
	}
	return unpackAddress(method, output)
}

func (writer *Writer) RunReconciler(ctx context.Context) {
	ticker := time.NewTicker(writer.config.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			requestContext, cancel := context.WithTimeout(ctx, writer.config.RequestTimeout)
			if !writer.Ready() {
				if err := writer.Recover(requestContext); err != nil {
					slog.Error("signer recovery failed", "error", err)
				}
			} else if err := writer.Reconcile(requestContext); err != nil {
				writer.ready.Store(false)
				slog.Error("signer reconciliation failed", "error", err)
			}
			cancel()
		}
	}
}

func (writer *Writer) finalityHead(ctx context.Context, tag gethrpc.BlockNumber) (*big.Int, error) {
	primary, err := writer.client.HeaderByNumber(ctx, big.NewInt(int64(tag)))
	if err != nil {
		return nil, err
	}
	secondary, err := writer.verifier.HeaderByNumber(ctx, big.NewInt(int64(tag)))
	if err != nil {
		return nil, err
	}
	if primary.Number.Cmp(secondary.Number) <= 0 {
		return new(big.Int).Set(primary.Number), nil
	}
	return new(big.Int).Set(secondary.Number), nil
}

func boundedError(err error) string {
	message := err.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

func isKnownTransaction(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "already known") || strings.Contains(message, "known transaction")
}

func isNonceConsumed(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "nonce too low") || strings.Contains(message, "already used")
}

func bumped(value *big.Int) *big.Int {
	result := new(big.Int).Mul(value, big.NewInt(9))
	result.Div(result, big.NewInt(8))
	if result.Cmp(value) <= 0 {
		result.Add(value, big.NewInt(1))
	}
	return result
}

func maximum(left, right *big.Int) *big.Int {
	if left.Cmp(right) >= 0 {
		return new(big.Int).Set(left)
	}
	return new(big.Int).Set(right)
}
