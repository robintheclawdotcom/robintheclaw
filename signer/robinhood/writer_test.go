package main

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

type fakeChain struct {
	chainID         *big.Int
	latest          *types.Header
	safe            *types.Header
	finalized       *types.Header
	headers         map[uint64]*types.Header
	codes           map[common.Address][]byte
	getters         map[common.Address]map[string]common.Address
	hashes          map[common.Address]map[string]common.Hash
	gas             uint64
	tip             *big.Int
	nonce           uint64
	balance         *big.Int
	receipts        map[common.Hash]*types.Receipt
	sent            []*types.Transaction
	sendHook        func()
	sendError       error
	finalityError   error
	simulationError error
}

func (chain *fakeChain) ChainID(context.Context) (*big.Int, error) {
	return new(big.Int).Set(chain.chainID), nil
}

func (chain *fakeChain) HeaderByNumber(_ context.Context, number *big.Int) (*types.Header, error) {
	if number == nil {
		return chain.latest, nil
	}
	if number.Int64() == int64(gethrpc.SafeBlockNumber) {
		if chain.finalityError != nil {
			return nil, chain.finalityError
		}
		return chain.safe, nil
	}
	if number.Int64() == int64(gethrpc.FinalizedBlockNumber) {
		if chain.finalityError != nil {
			return nil, chain.finalityError
		}
		return chain.finalized, nil
	}
	header, ok := chain.headers[number.Uint64()]
	if !ok {
		return nil, ethereum.NotFound
	}
	return header, nil
}

func (chain *fakeChain) CodeAt(_ context.Context, address common.Address, _ *big.Int) ([]byte, error) {
	return chain.codes[address], nil
}

func (chain *fakeChain) CallContract(_ context.Context, message ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	if len(message.Data) < 4 || message.To == nil {
		return nil, errors.New("invalid call")
	}
	if bytes.Equal(message.Data[:4], vaultABI.Methods["executeSpot"].ID) {
		return nil, chain.simulationError
	}
	for name, method := range vaultABI.Methods {
		if !bytes.Equal(message.Data[:4], method.ID) {
			continue
		}
		if value, ok := chain.getters[*message.To][name]; ok {
			return method.Outputs.Pack(value)
		}
		if value, ok := chain.hashes[*message.To][name]; ok {
			return method.Outputs.Pack(value)
		}
		return nil, errors.New("unknown getter")
	}
	return nil, errors.New("unknown call")
}

func (chain *fakeChain) EstimateGas(context.Context, ethereum.CallMsg) (uint64, error) {
	return chain.gas, nil
}

func (chain *fakeChain) SuggestGasTipCap(context.Context) (*big.Int, error) {
	return new(big.Int).Set(chain.tip), nil
}

func (chain *fakeChain) PendingNonceAt(context.Context, common.Address) (uint64, error) {
	return chain.nonce, nil
}

func (chain *fakeChain) BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error) {
	return new(big.Int).Set(chain.balance), nil
}

func (chain *fakeChain) SendTransaction(_ context.Context, transaction *types.Transaction) error {
	chain.sent = append(chain.sent, transaction)
	if chain.sendHook != nil {
		chain.sendHook()
	}
	return chain.sendError
}

func (chain *fakeChain) TransactionReceipt(_ context.Context, hash common.Hash) (*types.Receipt, error) {
	receipt, ok := chain.receipts[hash]
	if !ok {
		return nil, ethereum.NotFound
	}
	return receipt, nil
}

type writerFixture struct {
	config  Config
	chain   *fakeChain
	journal *fakeJournal
	signer  *KMSSigner
	writer  *Writer
}

func newWriterFixture(t *testing.T) writerFixture {
	t.Helper()
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := newKMSSigner(context.Background(), fakeKMS{private: private}, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	vault := common.HexToAddress("0x0000000000000000000000000000000000000011")
	risk := common.HexToAddress("0x0000000000000000000000000000000000000012")
	adapter := common.HexToAddress("0x0000000000000000000000000000000000000013")
	owner := common.HexToAddress("0x0000000000000000000000000000000000000014")
	factory := common.HexToAddress("0x0000000000000000000000000000000000000015")
	registry := common.HexToAddress("0x0000000000000000000000000000000000000016")
	settlement := common.HexToAddress("0x0000000000000000000000000000000000000017")
	vaultCode := []byte{1, 2, 3}
	riskCode := []byte{4, 5, 6}
	adapterCode := []byte{7, 8, 9}
	factoryCode := []byte{10, 11, 12}
	registryCode := []byte{13, 14, 15}
	policyDigest := common.HexToHash("0x" + strings.Repeat("a", 64))
	latest := testHeader(100)
	chain := &fakeChain{
		chainID:   big.NewInt(4663),
		latest:    latest,
		safe:      testHeader(90),
		finalized: testHeader(80),
		headers:   map[uint64]*types.Header{100: latest},
		codes: map[common.Address][]byte{
			vault: vaultCode, risk: riskCode, adapter: adapterCode,
			factory: factoryCode, registry: registryCode,
		},
		getters: map[common.Address]map[string]common.Address{
			vault: {
				"agent": signer.Address(), "riskManager": risk, "spotAdapter": adapter,
				"owner": owner, "registry": registry, "settlementAsset": settlement,
			},
			risk: {
				"executor": vault, "configAdmin": registry, "treasury": owner,
				"settlementAsset": settlement,
			},
			adapter: {"vault": vault, "configAdmin": registry, "settlementAsset": settlement},
			factory: {"registry": registry},
			registry: {
				"ownerOfVault": owner, "factoryOfVault": factory,
				"riskManagerOfVault": risk, "spotAdapterOfVault": adapter,
			},
		},
		hashes:   map[common.Address]map[string]common.Hash{factory: {"policyDigest": policyDigest}},
		gas:      100_000,
		tip:      big.NewInt(2),
		nonce:    7,
		balance:  big.NewInt(1_000_000_000),
		receipts: make(map[common.Hash]*types.Receipt),
	}
	config := Config{
		Enabled:             true,
		ExecutionAccountID:  "11111111-1111-4111-8111-111111111111",
		ChainID:             big.NewInt(4663),
		SignerAddress:       signer.Address(),
		OwnerAddress:        owner,
		FactoryAddress:      factory,
		FactoryCodeHash:     crypto.Keccak256Hash(factoryCode),
		RegistryAddress:     registry,
		RegistryCodeHash:    crypto.Keccak256Hash(registryCode),
		PolicyDigest:        policyDigest,
		VaultAddress:        vault,
		VaultCodeHash:       crypto.Keccak256Hash(vaultCode),
		RiskManagerAddress:  risk,
		RiskManagerCodeHash: crypto.Keccak256Hash(riskCode),
		SpotAdapterAddress:  adapter,
		SpotAdapterCodeHash: crypto.Keccak256Hash(adapterCode),
		MaxGasLimit:         500_000,
		MaxPriorityFee:      big.NewInt(20),
		MaxFeePerGas:        big.NewInt(100),
		MaxTransactionCost:  big.NewInt(20_000_000),
		MinimumGasReserve:   big.NewInt(1_000_000),
		MaxReplacementCount: 3,
		MaxReplacementAge:   10 * time.Minute,
	}
	journal := &fakeJournal{ready: true}
	writer := newWriter(config, chain, chain, signer, journal)
	writer.ready.Store(true)
	return writerFixture{config: config, chain: chain, journal: journal, signer: signer, writer: writer}
}

func TestSubmitPersistsBeforeBroadcast(t *testing.T) {
	fixture := newWriterFixture(t)
	submission, err := fixture.writer.Submit(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if submission.Status != "submitted" || fixture.journal.submitted != submission.RequestID {
		t.Fatalf("unexpected submission: %#v", submission)
	}
	if fixture.journal.reservation == nil || !fixture.journal.reservation.committed {
		t.Fatal("signed transaction was not committed with the nonce")
	}
	if len(fixture.chain.sent) != 1 || fixture.chain.sent[0].Hash() != common.HexToHash(submission.TxHash) {
		t.Fatal("journaled transaction was not broadcast")
	}
}

func TestSendTimeoutReturnsJournaledAmbiguity(t *testing.T) {
	fixture := newWriterFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.chain.sendHook = cancel
	fixture.chain.sendError = context.DeadlineExceeded

	first, err := fixture.writer.Submit(ctx, validRequest())
	var tracked *journaledSubmissionError
	if !errors.As(err, &tracked) {
		t.Fatalf("send timeout was not returned as a journaled outcome: %v", err)
	}
	if first.Status != "ambiguous" || tracked.Submission() != first {
		t.Fatalf("unexpected ambiguous submission: %#v", first)
	}
	if fixture.journal.reservation == nil || !fixture.journal.reservation.committed {
		t.Fatal("ambiguous transaction was not journaled before broadcast")
	}
	if fixture.writer.Ready() {
		t.Fatal("ambiguous broadcast did not latch readiness off")
	}

	retry, err := fixture.writer.Submit(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("idempotent retry failed while writer was unready: %v", err)
	}
	if retry != first {
		t.Fatalf("retry did not return the journaled submission: %#v", retry)
	}
	if len(fixture.chain.sent) != 1 {
		t.Fatalf("idempotent retry rebroadcast the transaction: %d", len(fixture.chain.sent))
	}
}

func TestCanceledRequestAfterBroadcastRecordsSubmitted(t *testing.T) {
	fixture := newWriterFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.chain.sendHook = cancel

	submission, err := fixture.writer.Submit(ctx, validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if submission.Status != SubmissionSubmitted {
		t.Fatalf("unexpected submission: %#v", submission)
	}
	if fixture.journal.submitted != submission.RequestID {
		t.Fatal("submitted transaction was not recorded after request cancellation")
	}
}

func TestSubmittedJournalFailureReturnsSignedRetry(t *testing.T) {
	fixture := newWriterFixture(t)
	fixture.journal.setSubmitted = errors.New("journal unavailable")

	first, err := fixture.writer.Submit(context.Background(), validRequest())
	var tracked *journaledSubmissionError
	if !errors.As(err, &tracked) {
		t.Fatalf("status update failure was not returned as a journaled outcome: %v", err)
	}
	if first.Status != "signed" || tracked.Submission() != first {
		t.Fatalf("unexpected signed submission: %#v", first)
	}
	if len(fixture.chain.sent) != 1 {
		t.Fatalf("transaction was not broadcast exactly once: %d", len(fixture.chain.sent))
	}

	retry, err := fixture.writer.Submit(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("signed retry failed while writer was unready: %v", err)
	}
	if retry != first {
		t.Fatalf("retry did not return the signed journal state: %#v", retry)
	}
	if len(fixture.chain.sent) != 1 {
		t.Fatalf("signed retry rebroadcast the transaction: %d", len(fixture.chain.sent))
	}
}

func TestRetryReturnsEveryJournaledSubmissionStatus(t *testing.T) {
	request := validRequest()
	_, _, digest, err := request.validate()
	if err != nil {
		t.Fatal(err)
	}
	statuses := []SubmissionStatus{
		SubmissionSigned,
		SubmissionSubmitted,
		SubmissionSoftConfirmed,
		SubmissionL1Posted,
		SubmissionEthereumFinal,
		SubmissionAmbiguous,
		SubmissionReplaced,
	}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			fixture := newWriterFixture(t)
			fixture.writer.ready.Store(false)
			expected := Submission{
				ExecutionAccountID: fixture.config.ExecutionAccountID,
				VaultAddress:       strings.ToLower(fixture.config.VaultAddress.Hex()),
				SignerAddress:      strings.ToLower(fixture.config.SignerAddress.Hex()),
				RequestID:          request.RequestID,
				IntentID:           request.Intent.ID,
				TxHash:             "0x" + strings.Repeat("a", 64),
				Nonce:              17,
				Status:             status,
			}
			fixture.journal.existing = map[string]fakeExisting{
				request.RequestID: {submission: expected, digest: digest},
			}

			actual, err := fixture.writer.Submit(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if actual != expected {
				t.Fatalf("journaled status changed: %#v", actual)
			}
			if fixture.journal.reservation != nil || len(fixture.chain.sent) != 0 {
				t.Fatal("idempotent retry entered the signing path")
			}
		})
	}
}

func TestPreflightRejectionNeverCreatesSubmission(t *testing.T) {
	fixture := newWriterFixture(t)
	fixture.chain.simulationError = errors.New("execution reverted")

	if submission, err := fixture.writer.Submit(context.Background(), validRequest()); err == nil {
		t.Fatalf("reverted simulation was accepted: %#v", submission)
	}
	if fixture.journal.reservation != nil || len(fixture.journal.existing) != 0 {
		t.Fatal("preflight rejection created a journaled submission")
	}
	if len(fixture.chain.sent) != 0 {
		t.Fatal("preflight rejection was broadcast")
	}
	if !fixture.writer.Ready() {
		t.Fatal("deterministic preflight rejection disabled the writer")
	}
}

func TestHostileFeeResponseRollsBackNonce(t *testing.T) {
	fixture := newWriterFixture(t)
	fixture.chain.tip = new(big.Int).Add(fixture.config.MaxPriorityFee, big.NewInt(1))
	if _, err := fixture.writer.Submit(context.Background(), validRequest()); err == nil {
		t.Fatal("priority fee above the configured cap was accepted")
	}
	if fixture.journal.reservation == nil || !fixture.journal.reservation.rolledBack {
		t.Fatal("rejected fee did not roll back the nonce transaction")
	}
	if len(fixture.chain.sent) != 0 {
		t.Fatal("rejected transaction was broadcast")
	}
}

func TestRecoveryStaysUnreadyUntilReconciliationSucceeds(t *testing.T) {
	fixture := newWriterFixture(t)
	fixture.writer.ready.Store(false)
	fixture.chain.finalityError = errors.New("finality unavailable")
	if err := fixture.writer.Recover(context.Background()); err == nil || fixture.writer.Ready() {
		t.Fatal("recovery fault did not latch readiness off")
	}
	fixture.chain.finalityError = nil
	if err := fixture.writer.Recover(context.Background()); err != nil || !fixture.writer.Ready() {
		t.Fatalf("successful recovery did not restore readiness: %v", err)
	}
}

func TestInvalidJournalRecordIsQuarantined(t *testing.T) {
	fixture := newWriterFixture(t)
	record := signedRecordFor(t, fixture, common.HexToAddress("0x0000000000000000000000000000000000000099"))
	fixture.journal.pending = []TransactionRecord{record}
	if err := fixture.writer.Reconcile(context.Background()); err == nil {
		t.Fatal("invalid journal record was accepted")
	}
	if fixture.journal.quarantined != record.RequestID {
		t.Fatal("invalid journal record was not quarantined")
	}
}

func TestCanonicalReceiptFinalizesOneNonceVariant(t *testing.T) {
	fixture := newWriterFixture(t)
	record := signedRecordFor(t, fixture, fixture.config.VaultAddress)
	header := testHeader(70)
	fixture.chain.headers[70] = header
	fixture.chain.receipts[record.TxHash] = &types.Receipt{
		TxHash: record.TxHash, BlockHash: header.Hash(), BlockNumber: big.NewInt(70),
		Status: types.ReceiptStatusSuccessful,
	}
	fixture.journal.pending = []TransactionRecord{record}
	if err := fixture.writer.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fixture.journal.receiptStatus != "soft_confirmed" || fixture.journal.superseded != record.RequestID {
		t.Fatal("canonical nonce winner was not reconciled")
	}
	if fixture.journal.finality != "ethereum_final" {
		t.Fatalf("unexpected finality: %s", fixture.journal.finality)
	}
}

func signedRecordFor(t *testing.T, fixture writerFixture, target common.Address) TransactionRecord {
	t.Helper()
	request := validRequest()
	intent, payload, digest, err := request.validate()
	if err != nil {
		t.Fatal(err)
	}
	data, err := packExecuteSpot(intent)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID: fixture.config.ChainID, Nonce: 7, GasTipCap: big.NewInt(2),
		GasFeeCap: big.NewInt(10), Gas: 120_000, To: &target, Data: data,
	})
	signer := types.LatestSignerForChainID(fixture.config.ChainID)
	signature, err := fixture.signer.SignDigest(context.Background(), signer.Hash(unsigned).Bytes())
	if err != nil {
		t.Fatal(err)
	}
	signed, err := unsigned.WithSignature(signer, signature)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return TransactionRecord{
		RequestID:     request.RequestID,
		IntentID:      strings.ToLower(common.BytesToHash(intent.ID[:]).Hex()),
		PayloadSHA256: digest,
		Nonce:         7,
		TxHash:        signed.Hash(),
		Status:        "submitted",
		SignedTx:      raw,
		Payload:       payload,
	}
}

func testHeader(number int64) *types.Header {
	return &types.Header{
		Number:   big.NewInt(number),
		GasLimit: 30_000_000,
		BaseFee:  big.NewInt(1),
		Time:     uint64(number),
		Extra:    []byte{byte(number)},
	}
}
