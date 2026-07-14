package aaplrelay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

var errNonceDrift = errors.New("AAPL relay publisher nonce drift")

type Service struct {
	config  Config
	target  Chain
	source  *SourceReader
	feed    *TargetFeed
	journal *Journal
	metrics *Metrics
	clock   func() time.Time
}

func NewService(
	config Config,
	target Chain,
	source *SourceReader,
	feed *TargetFeed,
	journal *Journal,
	metrics *Metrics,
) *Service {
	return &Service{
		config: config, target: target, source: source, feed: feed,
		journal: journal, metrics: metrics, clock: time.Now,
	}
}

func (service *Service) Verify(ctx context.Context) error {
	if service.journal == nil {
		return errors.New("AAPL relay journal is unavailable")
	}
	state, err := service.journal.State(ctx)
	if err != nil {
		return err
	}
	if state.QuarantinedReason != "" {
		return errors.New("AAPL relay publisher is quarantined")
	}
	if _, err := service.feed.Verify(ctx, service.target, service.config.SignerAddress); err != nil {
		return err
	}
	service.metrics.ready.Store(true)
	return nil
}

func (service *Service) Run(ctx context.Context) error {
	if err := service.Verify(ctx); err != nil {
		return err
	}
	service.runCycle(ctx)
	ticker := time.NewTicker(service.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			cycleContext, cancel := context.WithTimeout(ctx, service.config.RequestTimeout)
			service.runCycle(cycleContext)
			cancel()
		}
	}
}

func (service *Service) runCycle(ctx context.Context) {
	if err := service.cycle(ctx); err != nil {
		service.metrics.failures.Add(1)
		slog.Error("AAPL relay cycle failed", "publisher", service.config.PublisherID, "error", err)
	}
}

func (service *Service) cycle(ctx context.Context) error {
	service.metrics.lastCycle.Store(service.clock().UTC().Unix())
	state, err := service.journal.State(ctx)
	if err != nil {
		return err
	}
	if state.QuarantinedReason != "" {
		service.metrics.ready.Store(false)
		return errors.New("AAPL relay publisher is quarantined")
	}
	pending, err := service.journal.Pending(ctx)
	if err != nil {
		return err
	}
	if pending != nil {
		service.metrics.pending.Store(true)
		confirmed, err := service.reconcile(ctx, *pending)
		if err != nil || !confirmed {
			return err
		}
	}
	service.metrics.pending.Store(false)

	observation, err := service.source.Observe(ctx)
	if err != nil {
		service.metrics.sourceHealthy.Store(false)
		return err
	}
	service.metrics.sourceHealthy.Store(true)
	service.metrics.sourceRound.Store(observation.RoundID.Uint64())
	service.metrics.sourceUpdated.Store(int64(observation.UpdatedAt))
	state, err = service.journal.RecordObservation(ctx, observation)
	if err != nil {
		service.metrics.ready.Store(false)
		return err
	}
	onchain, err := service.feed.Verify(ctx, service.target, service.config.SignerAddress)
	if err != nil {
		service.metrics.ready.Store(false)
		return err
	}
	service.metrics.ready.Store(true)
	sequence := state.LastSequence
	if onchain.Sequence > sequence {
		sequence = onchain.Sequence
	}
	if sequence == ^uint64(0) {
		return errors.New("AAPL relay sequence exhausted")
	}
	pendingTx, err := service.sign(ctx, sequence+1, observation, state.LastNonce)
	if err != nil {
		if errors.Is(err, errNonceDrift) {
			return service.quarantine(ctx, common.Hash{}, err.Error())
		}
		return err
	}
	if err := service.journal.RecordSigned(ctx, pendingTx); err != nil {
		return err
	}
	service.metrics.pending.Store(true)
	return service.broadcast(ctx, pendingTx)
}

func (service *Service) sign(
	ctx context.Context,
	sequence uint64,
	observation PriceObservation,
	lastNonce *uint64,
) (PendingTransaction, error) {
	if service.clock().UTC().Sub(time.Unix(int64(observation.UpdatedAt), 0)) > MaxSourceAge {
		return PendingTransaction{}, errors.New("AAPL source expired before signing")
	}
	data, err := service.feed.PackReport(sequence, observation)
	if err != nil {
		return PendingTransaction{}, err
	}
	observedNonce, err := service.target.PendingNonceAt(ctx, service.config.SignerAddress)
	if err != nil {
		return PendingTransaction{}, errors.New("read AAPL relay publisher nonce")
	}
	expectedNonce := uint64(0)
	if lastNonce != nil {
		if *lastNonce == ^uint64(0) {
			return PendingTransaction{}, errors.New("AAPL relay publisher nonce exhausted")
		}
		expectedNonce = *lastNonce + 1
	}
	if observedNonce != expectedNonce {
		return PendingTransaction{}, fmt.Errorf(
			"%w: expected %d, observed %d", errNonceDrift, expectedNonce, observedNonce,
		)
	}
	nonce := expectedNonce
	call := ethereum.CallMsg{
		From: service.config.SignerAddress, To: &service.config.TargetFeed, Data: data,
	}
	estimated, err := service.target.EstimateGas(ctx, call)
	if err != nil {
		return PendingTransaction{}, errors.New("estimate AAPL relay report gas")
	}
	gasLimit := estimated + estimated/5
	if gasLimit < estimated || gasLimit > service.config.MaxGasLimit {
		return PendingTransaction{}, errors.New("AAPL relay report exceeds gas limit")
	}
	header, err := service.target.HeaderByNumber(ctx, nil)
	if err != nil || !validHeader(header) || header.BaseFee == nil {
		return PendingTransaction{}, errors.New("read AAPL relay fee head")
	}
	tip, err := service.target.SuggestGasTipCap(ctx)
	if err != nil || tip.Sign() <= 0 || tip.Cmp(service.config.MaxPriorityFee) > 0 {
		return PendingTransaction{}, errors.New("AAPL relay priority fee exceeds cap")
	}
	fee := new(big.Int).Add(new(big.Int).Mul(header.BaseFee, big.NewInt(2)), tip)
	if fee.Cmp(service.config.MaxFeePerGas) > 0 {
		return PendingTransaction{}, errors.New("AAPL relay total fee exceeds cap")
	}
	cost := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), fee)
	if cost.Cmp(service.config.MaxTransactionCost) > 0 {
		return PendingTransaction{}, errors.New("AAPL relay transaction cost exceeds cap")
	}
	balance, err := service.target.BalanceAt(ctx, service.config.SignerAddress, nil)
	if err != nil || balance.Cmp(new(big.Int).Add(cost, service.config.MinimumGasReserve)) < 0 {
		return PendingTransaction{}, errors.New("AAPL relay publisher gas balance is below reserve")
	}
	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID: new(big.Int).SetUint64(TargetChainID), Nonce: nonce,
		GasTipCap: tip, GasFeeCap: fee, Gas: gasLimit,
		To: &service.config.TargetFeed, Value: new(big.Int), Data: data,
	})
	signed, err := types.SignTx(
		unsigned,
		types.LatestSignerForChainID(new(big.Int).SetUint64(TargetChainID)),
		service.config.PrivateKey,
	)
	if err != nil {
		return PendingTransaction{}, errors.New("sign AAPL relay report")
	}
	if err := verifySignedReport(signed, service.config, service.feed, sequence, observation); err != nil {
		return PendingTransaction{}, err
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		return PendingTransaction{}, errors.New("encode signed AAPL relay report")
	}
	return PendingTransaction{
		Sequence: sequence, Nonce: nonce, Hash: signed.Hash(), Raw: raw,
		Observation: observation, Status: "signed", CreatedAt: service.clock().UTC(),
	}, nil
}

func (service *Service) broadcast(ctx context.Context, pending PendingTransaction) error {
	transaction := new(types.Transaction)
	if err := transaction.UnmarshalBinary(pending.Raw); err != nil || transaction.Hash() != pending.Hash {
		return service.quarantine(ctx, pending.Hash, "journaled AAPL relay transaction identity mismatch")
	}
	if err := verifySignedReport(transaction, service.config, service.feed, pending.Sequence, pending.Observation); err != nil {
		return service.quarantine(ctx, pending.Hash, err.Error())
	}
	if err := service.target.SendTransaction(ctx, transaction); err != nil {
		return fmt.Errorf("broadcast AAPL relay report %s: %w", pending.Hash.Hex(), err)
	}
	return service.journal.MarkSubmitted(ctx, pending.Hash)
}

func (service *Service) reconcile(ctx context.Context, pending PendingTransaction) (bool, error) {
	transaction := new(types.Transaction)
	if err := transaction.UnmarshalBinary(pending.Raw); err != nil || transaction.Hash() != pending.Hash {
		return false, service.quarantine(ctx, pending.Hash, "journaled AAPL relay transaction identity mismatch")
	}
	if err := verifySignedReport(transaction, service.config, service.feed, pending.Sequence, pending.Observation); err != nil {
		return false, service.quarantine(ctx, pending.Hash, err.Error())
	}
	receipt, err := service.target.TransactionReceipt(ctx, pending.Hash)
	if err == nil && receipt != nil {
		success := receipt.Status == types.ReceiptStatusSuccessful
		if err := service.journal.MarkReceipt(ctx, pending.Hash, success); err != nil {
			return false, err
		}
		service.metrics.pending.Store(false)
		if !success {
			return false, service.quarantine(ctx, common.Hash{}, "AAPL relay report transaction reverted")
		}
		service.metrics.confirmed.Add(1)
		service.metrics.lastConfirmed.Store(service.clock().UTC().Unix())
		return true, nil
	}
	if err != nil && !errors.Is(err, ethereum.NotFound) {
		return false, errors.New("read AAPL relay report receipt")
	}
	found, isPending, lookupErr := service.target.TransactionByHash(ctx, pending.Hash)
	if lookupErr == nil && found != nil {
		if found.Hash() != pending.Hash {
			return false, service.quarantine(ctx, pending.Hash, "AAPL relay lookup hash mismatch")
		}
		if !isPending {
			return false, errors.New("AAPL relay transaction is mined without a receipt")
		}
		return false, nil
	}
	if lookupErr != nil && !errors.Is(lookupErr, ethereum.NotFound) {
		return false, errors.New("lookup pending AAPL relay transaction")
	}
	nonce, err := service.target.PendingNonceAt(ctx, service.config.SignerAddress)
	if err != nil {
		return false, errors.New("reconcile AAPL relay publisher nonce")
	}
	if nonce > pending.Nonce {
		return false, service.quarantine(ctx, pending.Hash, "AAPL relay nonce advanced without journaled receipt")
	}
	return false, service.broadcast(ctx, pending)
}

func (service *Service) quarantine(ctx context.Context, hash common.Hash, reason string) error {
	service.metrics.ready.Store(false)
	if err := service.journal.Quarantine(ctx, hash, reason); err != nil {
		return err
	}
	return errors.New(reason)
}

func verifySignedReport(
	transaction *types.Transaction,
	config Config,
	feed *TargetFeed,
	sequence uint64,
	observation PriceObservation,
) error {
	if transaction.Type() != types.DynamicFeeTxType ||
		transaction.ChainId().Cmp(new(big.Int).SetUint64(TargetChainID)) != 0 {
		return errors.New("invalid AAPL relay transaction envelope")
	}
	if transaction.To() == nil || *transaction.To() != config.TargetFeed || transaction.Value().Sign() != 0 {
		return errors.New("invalid AAPL relay transaction destination")
	}
	expectedData, err := feed.PackReport(sequence, observation)
	if err != nil || !constantTimeEqual(transaction.Data(), expectedData) {
		return errors.New("invalid AAPL relay transaction calldata")
	}
	if transaction.Gas() > config.MaxGasLimit ||
		transaction.GasTipCap().Cmp(config.MaxPriorityFee) > 0 ||
		transaction.GasFeeCap().Cmp(config.MaxFeePerGas) > 0 {
		return errors.New("AAPL relay transaction exceeds fee bounds")
	}
	cost := new(big.Int).Mul(new(big.Int).SetUint64(transaction.Gas()), transaction.GasFeeCap())
	if cost.Cmp(config.MaxTransactionCost) > 0 {
		return errors.New("AAPL relay transaction exceeds cost bound")
	}
	sender, err := types.Sender(
		types.LatestSignerForChainID(new(big.Int).SetUint64(TargetChainID)), transaction,
	)
	if err != nil || sender != config.SignerAddress ||
		sender != crypto.PubkeyToAddress(config.PrivateKey.PublicKey) {
		return errors.New("AAPL relay transaction signer mismatch")
	}
	return nil
}

func constantTimeEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}
