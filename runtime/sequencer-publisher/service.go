package sequencerpublisher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type Metrics struct {
	ready         atomic.Bool
	sourceHealthy atomic.Bool
	lastCycle     atomic.Int64
	lastConfirmed atomic.Int64
	failures      atomic.Uint64
	confirmed     atomic.Uint64
	pending       atomic.Bool
}

type Service struct {
	config      Config
	source      Chain
	transaction Chain
	feed        *Feed
	prober      *Prober
	journal     *Journal
	metrics     *Metrics
	clock       func() time.Time
}

func NewService(config Config, source, transaction Chain, feed *Feed, journal *Journal, metrics *Metrics) *Service {
	return &Service{
		config: config, source: source, transaction: transaction, feed: feed,
		prober: NewProber(source, transaction, config), journal: journal, metrics: metrics,
		clock: time.Now,
	}
}

func (service *Service) Verify(ctx context.Context) error {
	if service.journal == nil {
		return errors.New("sequencer journal is unavailable")
	}
	state, err := service.journal.State(ctx)
	if err != nil {
		return err
	}
	if state.QuarantinedReason != "" {
		return errors.New("sequencer publisher is quarantined")
	}
	if _, err := service.feed.Verify(ctx, service.transaction, service.config.SignerAddress); err != nil {
		return err
	}
	service.metrics.ready.Store(true)
	return nil
}

func (service *Service) Run(ctx context.Context) error {
	if err := service.Verify(ctx); err != nil {
		return err
	}
	if err := service.cycle(ctx); err != nil {
		slog.Error("sequencer publisher cycle failed", "publisher", service.config.PublisherID, "error", err)
	}
	ticker := time.NewTicker(service.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			cycleContext, cancel := context.WithTimeout(ctx, service.config.RequestTimeout)
			err := service.cycle(cycleContext)
			cancel()
			if err != nil {
				service.metrics.failures.Add(1)
				slog.Error("sequencer publisher cycle failed", "publisher", service.config.PublisherID, "error", err)
			}
		}
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
		return errors.New("sequencer publisher is quarantined")
	}
	if pending, err := service.journal.Pending(ctx); err != nil {
		return err
	} else if pending != nil {
		service.metrics.pending.Store(true)
		confirmed, err := service.reconcile(ctx, *pending)
		if err != nil || !confirmed {
			return err
		}
	}
	service.metrics.pending.Store(false)

	observation := service.prober.Probe(ctx, state.Heads)
	service.metrics.sourceHealthy.Store(observation.Healthy)
	maxGap := minDuration(3*service.config.Interval, 55*time.Second)
	state, err = service.journal.RecordObservation(ctx, observation, maxGap)
	if err != nil {
		return err
	}
	onchain, err := service.feed.Verify(ctx, service.transaction, service.config.SignerAddress)
	if err != nil {
		service.metrics.ready.Store(false)
		return err
	}
	service.metrics.ready.Store(true)
	sequence := max(state.LastSequence, onchain.Sequence) + 1
	if sequence == 0 {
		return errors.New("sequencer report sequence exhausted")
	}
	pending, err := service.sign(ctx, sequence, observation.Healthy, state.ContinuousStartedAt, state.LastNonce)
	if err != nil {
		return err
	}
	if err := service.journal.RecordSigned(ctx, pending); err != nil {
		return err
	}
	service.metrics.pending.Store(true)
	return service.broadcast(ctx, pending)
}

func (service *Service) sign(
	ctx context.Context,
	sequence uint64,
	healthy bool,
	startedAt uint64,
	lastNonce *uint64,
) (PendingTransaction, error) {
	data, err := service.feed.PackReport(sequence, healthy, startedAt)
	if err != nil {
		return PendingTransaction{}, err
	}
	observedNonce, err := service.transaction.PendingNonceAt(ctx, service.config.SignerAddress)
	if err != nil {
		return PendingTransaction{}, errors.New("read sequencer publisher nonce")
	}
	nonce := observedNonce
	if lastNonce != nil && *lastNonce >= nonce {
		if *lastNonce == ^uint64(0) {
			return PendingTransaction{}, errors.New("sequencer publisher nonce exhausted")
		}
		nonce = *lastNonce + 1
	}
	call := ethereum.CallMsg{
		From: service.config.SignerAddress, To: &service.config.FeedAddress, Data: data,
	}
	estimated, err := service.transaction.EstimateGas(ctx, call)
	if err != nil {
		return PendingTransaction{}, errors.New("estimate sequencer report gas")
	}
	gasLimit := estimated + estimated/5
	if gasLimit < estimated || gasLimit > service.config.MaxGasLimit {
		return PendingTransaction{}, errors.New("sequencer report exceeds gas limit")
	}
	header, err := service.transaction.HeaderByNumber(ctx, nil)
	if err != nil || !validHeader(header) || header.BaseFee == nil {
		return PendingTransaction{}, errors.New("read sequencer transaction fee head")
	}
	tip, err := service.transaction.SuggestGasTipCap(ctx)
	if err != nil || tip.Sign() <= 0 || tip.Cmp(service.config.MaxPriorityFee) > 0 {
		return PendingTransaction{}, errors.New("sequencer priority fee exceeds cap")
	}
	fee := new(big.Int).Add(new(big.Int).Mul(header.BaseFee, big.NewInt(2)), tip)
	if fee.Cmp(service.config.MaxFeePerGas) > 0 {
		return PendingTransaction{}, errors.New("sequencer total fee exceeds cap")
	}
	cost := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), fee)
	if cost.Cmp(service.config.MaxTransactionCost) > 0 {
		return PendingTransaction{}, errors.New("sequencer transaction cost exceeds cap")
	}
	balance, err := service.transaction.BalanceAt(ctx, service.config.SignerAddress, nil)
	if err != nil || balance.Cmp(new(big.Int).Add(cost, service.config.MinimumGasReserve)) < 0 {
		return PendingTransaction{}, errors.New("sequencer publisher gas balance is below reserve")
	}
	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID: new(big.Int).SetUint64(chainID), Nonce: nonce,
		GasTipCap: tip, GasFeeCap: fee, Gas: gasLimit,
		To: &service.config.FeedAddress, Value: new(big.Int), Data: data,
	})
	signed, err := types.SignTx(unsigned, types.LatestSignerForChainID(new(big.Int).SetUint64(chainID)), service.config.PrivateKey)
	if err != nil {
		return PendingTransaction{}, errors.New("sign sequencer report")
	}
	if err := verifySignedReport(signed, service.config, service.feed, sequence, healthy, startedAt); err != nil {
		return PendingTransaction{}, err
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		return PendingTransaction{}, errors.New("encode signed sequencer report")
	}
	return PendingTransaction{
		Sequence: sequence, Nonce: nonce, Hash: signed.Hash(), Raw: raw,
		Healthy: healthy, StartedAt: startedAt, Status: "signed", CreatedAt: service.clock().UTC(),
	}, nil
}

func (service *Service) broadcast(ctx context.Context, pending PendingTransaction) error {
	transaction := new(types.Transaction)
	if err := transaction.UnmarshalBinary(pending.Raw); err != nil || transaction.Hash() != pending.Hash {
		return service.quarantine(ctx, pending.Hash, "journaled transaction identity mismatch")
	}
	if err := verifySignedReport(transaction, service.config, service.feed, pending.Sequence, pending.Healthy, pending.StartedAt); err != nil {
		return service.quarantine(ctx, pending.Hash, err.Error())
	}
	if err := service.transaction.SendTransaction(ctx, transaction); err != nil {
		return fmt.Errorf("broadcast sequencer report %s: %w", pending.Hash.Hex(), err)
	}
	if err := service.journal.MarkSubmitted(ctx, pending.Hash); err != nil {
		return err
	}
	return nil
}

func (service *Service) reconcile(ctx context.Context, pending PendingTransaction) (bool, error) {
	transaction := new(types.Transaction)
	if err := transaction.UnmarshalBinary(pending.Raw); err != nil || transaction.Hash() != pending.Hash {
		return false, service.quarantine(ctx, pending.Hash, "journaled transaction identity mismatch")
	}
	if err := verifySignedReport(transaction, service.config, service.feed, pending.Sequence, pending.Healthy, pending.StartedAt); err != nil {
		return false, service.quarantine(ctx, pending.Hash, err.Error())
	}
	receipt, err := service.transaction.TransactionReceipt(ctx, pending.Hash)
	if err == nil && receipt != nil {
		if err := service.journal.MarkReceipt(ctx, pending.Hash, receipt.Status == types.ReceiptStatusSuccessful); err != nil {
			return false, err
		}
		service.metrics.pending.Store(false)
		if receipt.Status != types.ReceiptStatusSuccessful {
			return false, errors.New("sequencer report transaction reverted")
		}
		service.metrics.confirmed.Add(1)
		service.metrics.lastConfirmed.Store(service.clock().UTC().Unix())
		return true, nil
	}
	if err != nil && !errors.Is(err, ethereum.NotFound) {
		return false, errors.New("read sequencer report receipt")
	}
	found, isPending, lookupErr := service.transaction.TransactionByHash(ctx, pending.Hash)
	if lookupErr == nil && found != nil {
		if found.Hash() != pending.Hash {
			return false, service.quarantine(ctx, pending.Hash, "transaction lookup hash mismatch")
		}
		if !isPending {
			return false, errors.New("sequencer transaction is mined without a receipt")
		}
		return false, nil
	}
	if lookupErr != nil && !errors.Is(lookupErr, ethereum.NotFound) {
		return false, errors.New("lookup pending sequencer transaction")
	}
	nonce, err := service.transaction.PendingNonceAt(ctx, service.config.SignerAddress)
	if err != nil {
		return false, errors.New("reconcile sequencer publisher nonce")
	}
	if nonce > pending.Nonce {
		return false, service.quarantine(ctx, pending.Hash, "publisher nonce advanced without journaled receipt")
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
	feed *Feed,
	sequence uint64,
	healthy bool,
	startedAt uint64,
) error {
	if transaction.Type() != types.DynamicFeeTxType || transaction.ChainId().Cmp(new(big.Int).SetUint64(chainID)) != 0 {
		return errors.New("invalid sequencer transaction envelope")
	}
	if transaction.To() == nil || *transaction.To() != config.FeedAddress || transaction.Value().Sign() != 0 {
		return errors.New("invalid sequencer transaction destination")
	}
	expectedData, err := feed.PackReport(sequence, healthy, startedAt)
	if err != nil || !bytesEqual(transaction.Data(), expectedData) {
		return errors.New("invalid sequencer transaction calldata")
	}
	if transaction.Gas() > config.MaxGasLimit || transaction.GasTipCap().Cmp(config.MaxPriorityFee) > 0 ||
		transaction.GasFeeCap().Cmp(config.MaxFeePerGas) > 0 {
		return errors.New("sequencer transaction exceeds fee bounds")
	}
	cost := new(big.Int).Mul(new(big.Int).SetUint64(transaction.Gas()), transaction.GasFeeCap())
	if cost.Cmp(config.MaxTransactionCost) > 0 {
		return errors.New("sequencer transaction exceeds cost bound")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(new(big.Int).SetUint64(chainID)), transaction)
	if err != nil || sender != config.SignerAddress || sender != crypto.PubkeyToAddress(config.PrivateKey.PublicKey) {
		return errors.New("sequencer transaction signer mismatch")
	}
	return nil
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}
