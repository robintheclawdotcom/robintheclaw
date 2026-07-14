package sequencerpublisher

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type fakeChain struct {
	id        *big.Int
	latest    *types.Header
	finalized *types.Header
	headers   map[uint64]*types.Header
}

func (chain *fakeChain) ChainID(context.Context) (*big.Int, error) {
	return new(big.Int).Set(chain.id), nil
}

func (chain *fakeChain) HeaderByNumber(_ context.Context, number *big.Int) (*types.Header, error) {
	if number == nil {
		return chain.latest, nil
	}
	if number.Int64() == -3 {
		return chain.finalized, nil
	}
	header := chain.headers[number.Uint64()]
	if header == nil {
		return nil, ethereum.NotFound
	}
	return header, nil
}

func (*fakeChain) CodeAt(context.Context, common.Address, *big.Int) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (*fakeChain) CallContract(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (*fakeChain) EstimateGas(context.Context, ethereum.CallMsg) (uint64, error) {
	return 0, errors.New("not implemented")
}

func (*fakeChain) SuggestGasTipCap(context.Context) (*big.Int, error) {
	return nil, errors.New("not implemented")
}

func (*fakeChain) PendingNonceAt(context.Context, common.Address) (uint64, error) {
	return 0, errors.New("not implemented")
}

func (*fakeChain) BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error) {
	return nil, errors.New("not implemented")
}

func (*fakeChain) SendTransaction(context.Context, *types.Transaction) error {
	return errors.New("not implemented")
}

func (*fakeChain) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	return nil, ethereum.NotFound
}

func (*fakeChain) TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error) {
	return nil, false, ethereum.NotFound
}

func TestProberAcceptsFreshMonotonicCommonView(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	finalized := testHeader(100, now.Add(-20*time.Second), "finalized")
	latest := testHeader(105, now.Add(-2*time.Second), "latest")
	source := &fakeChain{id: new(big.Int).SetUint64(chainID), latest: latest, finalized: finalized}
	transaction := &fakeChain{id: new(big.Int).SetUint64(chainID), headers: map[uint64]*types.Header{100: finalized}}
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if !observation.Healthy || observation.Reason != "healthy" {
		t.Fatalf("expected healthy observation, got %#v", observation)
	}
	if observation.Heads.LatestNumber != 105 || observation.Heads.FinalizedNumber != 100 {
		t.Fatalf("unexpected heads: %#v", observation.Heads)
	}
}

func TestProberFailsClosedOnRegression(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	finalized := testHeader(100, now.Add(-20*time.Second), "finalized")
	latest := testHeader(105, now.Add(-2*time.Second), "latest")
	source := &fakeChain{id: new(big.Int).SetUint64(chainID), latest: latest, finalized: finalized}
	transaction := &fakeChain{id: new(big.Int).SetUint64(chainID), headers: map[uint64]*types.Header{100: finalized}}
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
	})
	prober.clock = func() time.Time { return now }
	previous := HeadState{LatestNumber: 106, LatestHash: testHeader(106, now, "prior").Hash()}

	observation := prober.Probe(context.Background(), previous)
	if observation.Healthy || observation.Reason != "latest_regression" {
		t.Fatalf("expected latest regression, got %#v", observation)
	}
}

func TestProberFailsClosedOnRPCDisagreement(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	finalized := testHeader(100, now.Add(-20*time.Second), "source")
	other := testHeader(100, now.Add(-20*time.Second), "transaction")
	source := &fakeChain{
		id: new(big.Int).SetUint64(chainID), latest: testHeader(105, now.Add(-2*time.Second), "latest"), finalized: finalized,
	}
	transaction := &fakeChain{id: new(big.Int).SetUint64(chainID), headers: map[uint64]*types.Header{100: other}}
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if observation.Healthy || observation.Reason != "rpc_disagreement" {
		t.Fatalf("expected RPC disagreement, got %#v", observation)
	}
}

func TestProberFailsClosedOnStaleLatest(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	finalized := testHeader(100, now.Add(-20*time.Second), "finalized")
	source := &fakeChain{
		id: new(big.Int).SetUint64(chainID), latest: testHeader(105, now.Add(-31*time.Second), "latest"), finalized: finalized,
	}
	transaction := &fakeChain{id: new(big.Int).SetUint64(chainID), headers: map[uint64]*types.Header{100: finalized}}
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if observation.Healthy || observation.Reason != "latest_stale" {
		t.Fatalf("expected stale latest, got %#v", observation)
	}
}

func testHeader(number uint64, timestamp time.Time, marker string) *types.Header {
	return &types.Header{
		Number: new(big.Int).SetUint64(number), Time: uint64(timestamp.Unix()),
		GasLimit: 30_000_000, BaseFee: big.NewInt(1_000_000), Extra: []byte(marker),
	}
}
