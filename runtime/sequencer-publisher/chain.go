package sequencerpublisher

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const feedABIJSON = `[
  {"type":"function","name":"report","stateMutability":"nonpayable","inputs":[{"name":"sequence","type":"uint64"},{"name":"healthy","type":"bool"},{"name":"startedAt","type":"uint64"}],"outputs":[]},
  {"type":"function","name":"reports","stateMutability":"view","inputs":[{"name":"publisher","type":"address"}],"outputs":[{"name":"sequence","type":"uint64"},{"name":"startedAt","type":"uint64"},{"name":"updatedAt","type":"uint64"},{"name":"healthy","type":"bool"}]},
  {"type":"function","name":"publisher1","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"publisher2","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"publisher3","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"publisherCount","stateMutability":"view","inputs":[],"outputs":[{"type":"uint8"}]},
  {"type":"function","name":"quorum","stateMutability":"view","inputs":[],"outputs":[{"type":"uint8"}]},
  {"type":"function","name":"maxAge","stateMutability":"view","inputs":[],"outputs":[{"type":"uint64"}]},
  {"type":"function","name":"decimals","stateMutability":"view","inputs":[],"outputs":[{"type":"uint8"}]}
]`

type Chain interface {
	ChainID(context.Context) (*big.Int, error)
	HeaderByNumber(context.Context, *big.Int) (*types.Header, error)
	CodeAt(context.Context, common.Address, *big.Int) ([]byte, error)
	StorageAt(context.Context, common.Address, common.Hash, *big.Int) ([]byte, error)
	CallContract(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error)
	EstimateGas(context.Context, ethereum.CallMsg) (uint64, error)
	SuggestGasTipCap(context.Context) (*big.Int, error)
	PendingNonceAt(context.Context, common.Address) (uint64, error)
	BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error)
	SendTransaction(context.Context, *types.Transaction) error
	TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error)
	TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error)
}

type Feed struct {
	address  common.Address
	codeHash common.Hash
	contract abi.ABI
}

type FeedReport struct {
	Sequence  uint64
	StartedAt uint64
	UpdatedAt uint64
	Healthy   bool
}

func NewFeed(address common.Address, codeHash common.Hash) (*Feed, error) {
	contract, err := abi.JSON(strings.NewReader(feedABIJSON))
	if err != nil {
		return nil, errors.New("parse sequencer feed ABI")
	}
	return &Feed{address: address, codeHash: codeHash, contract: contract}, nil
}

func (feed *Feed) Verify(ctx context.Context, chain Chain, signer common.Address) (FeedReport, error) {
	id, err := chain.ChainID(ctx)
	if err != nil || id.Cmp(new(big.Int).SetUint64(chainID)) != 0 {
		return FeedReport{}, errors.New("transaction RPC chain ID mismatch")
	}
	head, err := chain.HeaderByNumber(ctx, nil)
	if err != nil || head == nil || head.Number == nil {
		return FeedReport{}, errors.New("read transaction RPC head")
	}
	code, err := chain.CodeAt(ctx, feed.address, head.Number)
	if err != nil || len(code) == 0 || crypto.Keccak256Hash(code) != feed.codeHash {
		return FeedReport{}, errors.New("sequencer feed runtime code mismatch")
	}

	publishers := make([]common.Address, 0, 3)
	for _, method := range []string{"publisher1", "publisher2", "publisher3"} {
		values, err := feed.call(ctx, chain, method)
		if err != nil || len(values) != 1 {
			return FeedReport{}, fmt.Errorf("read sequencer feed %s", method)
		}
		publisher, ok := values[0].(common.Address)
		if !ok || publisher == (common.Address{}) {
			return FeedReport{}, fmt.Errorf("invalid sequencer feed %s", method)
		}
		publishers = append(publishers, publisher)
	}
	matches := 0
	for _, publisher := range publishers {
		if publisher == signer {
			matches++
		}
	}
	if matches != 1 || publishers[0] == publishers[1] || publishers[0] == publishers[2] || publishers[1] == publishers[2] {
		return FeedReport{}, errors.New("publisher key is not uniquely bound to sequencer feed")
	}
	for method, expected := range map[string]uint64{"publisherCount": 3, "quorum": 2, "maxAge": 60, "decimals": 0} {
		values, err := feed.call(ctx, chain, method)
		if err != nil || len(values) != 1 || !uintValueEquals(values[0], expected) {
			return FeedReport{}, fmt.Errorf("sequencer feed %s mismatch", method)
		}
	}
	return feed.ReadReport(ctx, chain, signer)
}

func (feed *Feed) ReadReport(ctx context.Context, chain Chain, signer common.Address) (FeedReport, error) {
	values, err := feed.call(ctx, chain, "reports", signer)
	if err != nil || len(values) != 4 {
		return FeedReport{}, errors.New("read publisher report")
	}
	sequence, okSequence := values[0].(uint64)
	startedAt, okStarted := values[1].(uint64)
	updatedAt, okUpdated := values[2].(uint64)
	healthy, okHealthy := values[3].(bool)
	if !okSequence || !okStarted || !okUpdated || !okHealthy {
		return FeedReport{}, errors.New("decode publisher report")
	}
	return FeedReport{Sequence: sequence, StartedAt: startedAt, UpdatedAt: updatedAt, Healthy: healthy}, nil
}

func (feed *Feed) PackReport(sequence uint64, healthy bool, startedAt uint64) ([]byte, error) {
	if sequence == 0 || startedAt == 0 {
		return nil, errors.New("invalid sequencer report")
	}
	return feed.contract.Pack("report", sequence, healthy, startedAt)
}

func (feed *Feed) call(ctx context.Context, chain Chain, method string, args ...any) ([]any, error) {
	data, err := feed.contract.Pack(method, args...)
	if err != nil {
		return nil, err
	}
	result, err := chain.CallContract(ctx, ethereum.CallMsg{To: &feed.address, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	return feed.contract.Unpack(method, result)
}

func uintValueEquals(value any, expected uint64) bool {
	switch typed := value.(type) {
	case uint8:
		return uint64(typed) == expected
	case uint64:
		return typed == expected
	case *big.Int:
		return typed.IsUint64() && typed.Uint64() == expected
	default:
		return false
	}
}

type HeadState struct {
	LatestNumber    uint64
	LatestHash      common.Hash
	FinalizedNumber uint64
	FinalizedHash   common.Hash
}

type Observation struct {
	Healthy bool
	Reason  string
	Heads   HeadState
	At      time.Time
}

type Prober struct {
	source          Chain
	transaction     Chain
	latestMaxAge    time.Duration
	finalizedMaxAge time.Duration
	maxFinalizedLag uint64
	dependencies    DependencyPins
	clock           func() time.Time
}

func NewProber(source, transaction Chain, config Config) *Prober {
	return &Prober{
		source: source, transaction: transaction,
		latestMaxAge: config.LatestMaxAge, finalizedMaxAge: config.FinalizedMaxAge,
		maxFinalizedLag: config.MaxFinalizedLag, dependencies: config.Dependencies, clock: time.Now,
	}
}

func (prober *Prober) Probe(ctx context.Context, previous HeadState) Observation {
	now := prober.clock().UTC()
	fail := func(reason string) Observation {
		return Observation{Healthy: false, Reason: reason, Heads: previous, At: now}
	}
	id, err := prober.source.ChainID(ctx)
	if err != nil || id.Cmp(new(big.Int).SetUint64(chainID)) != 0 {
		return fail("source_chain_id")
	}
	latest, err := prober.source.HeaderByNumber(ctx, nil)
	if err != nil || !validHeader(latest) {
		return fail("latest_unavailable")
	}
	sourceFinalized, err := prober.source.HeaderByNumber(ctx, big.NewInt(-3))
	if err != nil || !validHeader(sourceFinalized) {
		return fail("finalized_unavailable")
	}
	if latest.Time > uint64(now.Add(5*time.Second).Unix()) || now.Sub(time.Unix(int64(latest.Time), 0)) > prober.latestMaxAge {
		return fail("latest_stale")
	}
	transactionFinalized, err := prober.transaction.HeaderByNumber(ctx, big.NewInt(-3))
	if err != nil || !validHeader(transactionFinalized) {
		return fail("transaction_finalized_unavailable")
	}
	transactionLatest, err := prober.transaction.HeaderByNumber(ctx, nil)
	if err != nil || !validHeader(transactionLatest) {
		return fail("transaction_latest_unavailable")
	}
	if sourceFinalized.Number.Cmp(latest.Number) > 0 ||
		transactionFinalized.Number.Cmp(transactionLatest.Number) > 0 {
		return fail("finalized_rpc_disagreement")
	}
	finalizedSkew := new(big.Int).Sub(sourceFinalized.Number, transactionFinalized.Number)
	finalizedSkew.Abs(finalizedSkew)
	if !finalizedSkew.IsUint64() || finalizedSkew.Uint64() > prober.maxFinalizedLag {
		return fail("finalized_rpc_skew")
	}
	commonFinalizedNumber := new(big.Int).Set(sourceFinalized.Number)
	if transactionFinalized.Number.Cmp(commonFinalizedNumber) < 0 {
		commonFinalizedNumber.Set(transactionFinalized.Number)
	}
	sourceCommonFinalized, err := headerAt(
		ctx,
		prober.source,
		sourceFinalized,
		commonFinalizedNumber,
	)
	if err != nil || !validHeader(sourceCommonFinalized) {
		return fail("finalized_common_unavailable")
	}
	transactionCommonFinalized, err := headerAt(
		ctx,
		prober.transaction,
		transactionFinalized,
		commonFinalizedNumber,
	)
	if err != nil || !validHeader(transactionCommonFinalized) ||
		transactionCommonFinalized.Hash() != sourceCommonFinalized.Hash() {
		return fail("finalized_rpc_disagreement")
	}
	commonLatestNumber := new(big.Int).Set(latest.Number)
	if transactionLatest.Number.Cmp(commonLatestNumber) < 0 {
		commonLatestNumber.Set(transactionLatest.Number)
	}
	sourceCommonLatest, err := headerAt(ctx, prober.source, latest, commonLatestNumber)
	if err != nil || !validHeader(sourceCommonLatest) {
		return fail("latest_common_unavailable")
	}
	transactionCommonLatest, err := headerAt(
		ctx,
		prober.transaction,
		transactionLatest,
		commonLatestNumber,
	)
	if err != nil || !validHeader(transactionCommonLatest) ||
		transactionCommonLatest.Hash() != sourceCommonLatest.Hash() {
		return fail("latest_rpc_disagreement")
	}
	commonLatestTime := time.Unix(int64(sourceCommonLatest.Time), 0)
	if sourceCommonLatest.Time > uint64(now.Add(5*time.Second).Unix()) ||
		now.Sub(commonLatestTime) > prober.latestMaxAge {
		return fail("latest_common_stale")
	}
	commonFinalizedTime := time.Unix(int64(sourceCommonFinalized.Time), 0)
	if sourceCommonFinalized.Time > uint64(now.Add(5*time.Second).Unix()) ||
		now.Sub(commonFinalizedTime) > prober.finalizedMaxAge {
		return fail("finalized_stale")
	}
	if commonFinalizedNumber.Cmp(commonLatestNumber) > 0 ||
		commonLatestNumber.Uint64()-commonFinalizedNumber.Uint64() > prober.maxFinalizedLag {
		return fail("finalized_lag")
	}
	if commonLatestNumber.Cmp(commonFinalizedNumber) == 0 &&
		sourceCommonLatest.Hash() != sourceCommonFinalized.Hash() {
		return fail("head_hash_mismatch")
	}
	if previous.LatestNumber != 0 && (commonLatestNumber.Uint64() < previous.LatestNumber ||
		(commonLatestNumber.Uint64() == previous.LatestNumber &&
			sourceCommonLatest.Hash() != previous.LatestHash)) {
		return fail("latest_regression")
	}
	if previous.FinalizedNumber != 0 &&
		(commonFinalizedNumber.Uint64() < previous.FinalizedNumber ||
			(commonFinalizedNumber.Uint64() == previous.FinalizedNumber &&
				sourceCommonFinalized.Hash() != previous.FinalizedHash)) {
		return fail("finalized_regression")
	}
	if reason := verifyDependencies(
		ctx,
		prober.source,
		prober.transaction,
		commonFinalizedNumber,
		prober.dependencies,
	); reason != "" {
		return fail(reason)
	}
	if reason := verifyDependencies(
		ctx,
		prober.source,
		prober.transaction,
		commonLatestNumber,
		prober.dependencies,
	); reason != "" {
		return fail(reason)
	}
	return Observation{
		Healthy: true,
		Reason:  "healthy",
		Heads: HeadState{
			LatestNumber: commonLatestNumber.Uint64(), LatestHash: sourceCommonLatest.Hash(),
			FinalizedNumber: commonFinalizedNumber.Uint64(), FinalizedHash: sourceCommonFinalized.Hash(),
		},
		At: now,
	}
}

func headerAt(
	ctx context.Context,
	chain Chain,
	known *types.Header,
	number *big.Int,
) (*types.Header, error) {
	if known.Number.Cmp(number) == 0 {
		return known, nil
	}
	return chain.HeaderByNumber(ctx, number)
}

func validHeader(header *types.Header) bool {
	return header != nil && header.Number != nil && header.Number.IsUint64() && header.Time != 0
}
