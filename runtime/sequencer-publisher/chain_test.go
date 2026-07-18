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
	"github.com/ethereum/go-ethereum/crypto"
)

type fakeChain struct {
	id        *big.Int
	latest    *types.Header
	finalized *types.Header
	headers   map[uint64]*types.Header
	code      map[common.Address][]byte
	storage   map[common.Address]map[common.Hash][]byte
	storageAt map[uint64]map[common.Address]map[common.Hash][]byte
	calls     map[common.Address][]byte
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

func (chain *fakeChain) CodeAt(_ context.Context, address common.Address, _ *big.Int) ([]byte, error) {
	code := chain.code[address]
	if len(code) == 0 {
		return nil, ethereum.NotFound
	}
	return code, nil
}

func (chain *fakeChain) StorageAt(
	_ context.Context,
	address common.Address,
	slot common.Hash,
	block *big.Int,
) ([]byte, error) {
	if block != nil {
		if value := chain.storageAt[block.Uint64()][address][slot]; len(value) != 0 {
			return value, nil
		}
	}
	value := chain.storage[address][slot]
	if len(value) == 0 {
		return nil, ethereum.NotFound
	}
	return value, nil
}

func (chain *fakeChain) CallContract(_ context.Context, message ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	if message.To == nil {
		return nil, errors.New("missing destination")
	}
	value := chain.calls[*message.To]
	if len(value) == 0 {
		return nil, ethereum.NotFound
	}
	return value, nil
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
	pins := seedDependencies(source, transaction)
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
		Dependencies: pins,
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

func TestProberAcceptsObservedNitroFinalityWindow(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	finalized := testHeader(12_071_740, now.Add(-940*time.Second), "finalized")
	latest := testHeader(12_081_090, now, "latest")
	source := &fakeChain{id: new(big.Int).SetUint64(chainID), latest: latest, finalized: finalized}
	transaction := &fakeChain{
		id:      new(big.Int).SetUint64(chainID),
		headers: map[uint64]*types.Header{finalized.Number.Uint64(): finalized},
	}
	pins := seedDependencies(source, transaction)
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 30 * time.Minute, MaxFinalizedLag: 25_000,
		Dependencies: pins,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if !observation.Healthy || observation.Reason != "healthy" {
		t.Fatalf("expected observed Nitro finality window to be healthy, got %#v", observation)
	}
}

func TestProberAcceptsLaggedProviderAtFreshCommonLatest(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	finalized := testHeader(100, now.Add(-20*time.Second), "finalized")
	sourceCommon := testHeader(103, now.Add(-4*time.Second), "common")
	source := &fakeChain{
		id:        new(big.Int).SetUint64(chainID),
		latest:    testHeader(105, now.Add(-2*time.Second), "source-latest"),
		finalized: finalized,
		headers:   map[uint64]*types.Header{103: sourceCommon},
	}
	transaction := &fakeChain{
		id:      new(big.Int).SetUint64(chainID),
		latest:  sourceCommon,
		headers: map[uint64]*types.Header{100: finalized},
	}
	pins := seedDependencies(source, transaction)
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
		Dependencies: pins,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if !observation.Healthy || observation.Reason != "healthy" {
		t.Fatalf("expected lagged provider to share a fresh latest view, got %#v", observation)
	}
	if observation.Heads.LatestNumber != sourceCommon.Number.Uint64() ||
		observation.Heads.LatestHash != sourceCommon.Hash() {
		t.Fatalf("expected lower common latest head, got %#v", observation.Heads)
	}
}

func TestProberUsesLowerIndependentlyFinalizedView(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	commonFinalized := testHeader(100, now.Add(-20*time.Second), "common-finalized")
	latest := testHeader(105, now.Add(-2*time.Second), "latest")
	source := &fakeChain{
		id:        new(big.Int).SetUint64(chainID),
		latest:    latest,
		finalized: latest,
		headers:   map[uint64]*types.Header{100: commonFinalized},
	}
	transaction := &fakeChain{
		id:        new(big.Int).SetUint64(chainID),
		latest:    latest,
		finalized: commonFinalized,
	}
	pins := seedDependencies(source, transaction)
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
		Dependencies: pins,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if !observation.Healthy || observation.Reason != "healthy" {
		t.Fatalf("expected lower finalized quorum view to be healthy, got %#v", observation)
	}
	if observation.Heads.FinalizedNumber != commonFinalized.Number.Uint64() ||
		observation.Heads.FinalizedHash != commonFinalized.Hash() {
		t.Fatalf("single-provider finality label advanced common head: %#v", observation.Heads)
	}
}

func TestProberFailsClosedOnFinalizedCommonHashDisagreement(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	sourceCommon := testHeader(100, now.Add(-20*time.Second), "source-common")
	transactionCommon := testHeader(100, now.Add(-20*time.Second), "transaction-common")
	latest := testHeader(105, now.Add(-2*time.Second), "latest")
	source := &fakeChain{
		id:        new(big.Int).SetUint64(chainID),
		latest:    latest,
		finalized: latest,
		headers:   map[uint64]*types.Header{100: sourceCommon},
	}
	transaction := &fakeChain{
		id:        new(big.Int).SetUint64(chainID),
		latest:    latest,
		finalized: transactionCommon,
	}
	pins := seedDependencies(source, transaction)
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
		Dependencies: pins,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if observation.Healthy || observation.Reason != "finalized_rpc_disagreement" {
		t.Fatalf("expected finalized RPC disagreement, got %#v", observation)
	}
}

func TestProberFailsClosedOnFinalizedRPCSkew(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	latest := testHeader(300, now.Add(-2*time.Second), "latest")
	source := &fakeChain{
		id:        new(big.Int).SetUint64(chainID),
		latest:    latest,
		finalized: testHeader(250, now.Add(-10*time.Second), "source-finalized"),
	}
	transaction := &fakeChain{
		id:        new(big.Int).SetUint64(chainID),
		latest:    latest,
		finalized: testHeader(100, now.Add(-20*time.Second), "transaction-finalized"),
	}
	pins := seedDependencies(source, transaction)
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
		Dependencies: pins,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if observation.Healthy || observation.Reason != "finalized_rpc_skew" {
		t.Fatalf("expected finalized RPC skew, got %#v", observation)
	}
}

func TestProberFailsClosedOutsideFinalityWindow(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	tests := []struct {
		name      string
		latest    uint64
		finalized uint64
		age       time.Duration
		reason    string
	}{
		{name: "age", latest: 30_000, finalized: 20_000, age: 30*time.Minute + time.Second, reason: "finalized_stale"},
		{name: "lag", latest: 45_001, finalized: 20_000, age: 20 * time.Minute, reason: "finalized_lag"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			finalized := testHeader(test.finalized, now.Add(-test.age), "finalized")
			source := &fakeChain{
				id:        new(big.Int).SetUint64(chainID),
				latest:    testHeader(test.latest, now, "latest"),
				finalized: finalized,
			}
			transaction := &fakeChain{
				id:      new(big.Int).SetUint64(chainID),
				headers: map[uint64]*types.Header{test.finalized: finalized},
			}
			pins := seedDependencies(source, transaction)
			prober := NewProber(source, transaction, Config{
				LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 30 * time.Minute, MaxFinalizedLag: 25_000,
				Dependencies: pins,
			})
			prober.clock = func() time.Time { return now }

			observation := prober.Probe(context.Background(), HeadState{})
			if observation.Healthy || observation.Reason != test.reason {
				t.Fatalf("expected %s, got %#v", test.reason, observation)
			}
		})
	}
}

func TestProberFailsClosedOnRegression(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	finalized := testHeader(100, now.Add(-20*time.Second), "finalized")
	latest := testHeader(105, now.Add(-2*time.Second), "latest")
	source := &fakeChain{id: new(big.Int).SetUint64(chainID), latest: latest, finalized: finalized}
	transaction := &fakeChain{id: new(big.Int).SetUint64(chainID), headers: map[uint64]*types.Header{100: finalized}}
	pins := seedDependencies(source, transaction)
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
		Dependencies: pins,
	})
	prober.clock = func() time.Time { return now }
	previous := HeadState{LatestNumber: 106, LatestHash: testHeader(106, now, "prior").Hash()}

	observation := prober.Probe(context.Background(), previous)
	if observation.Healthy || observation.Reason != "latest_regression" {
		t.Fatalf("expected latest regression, got %#v", observation)
	}
}

func TestProberFailsClosedOnFinalizedRPCDisagreement(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	finalized := testHeader(100, now.Add(-20*time.Second), "source")
	other := testHeader(100, now.Add(-20*time.Second), "transaction")
	source := &fakeChain{
		id: new(big.Int).SetUint64(chainID), latest: testHeader(105, now.Add(-2*time.Second), "latest"), finalized: finalized,
	}
	transaction := &fakeChain{id: new(big.Int).SetUint64(chainID), finalized: other}
	pins := seedDependencies(source, transaction)
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
		Dependencies: pins,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if observation.Healthy || observation.Reason != "finalized_rpc_disagreement" {
		t.Fatalf("expected finalized RPC disagreement, got %#v", observation)
	}
}

func TestProberFailsClosedOnStaleLatest(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	finalized := testHeader(100, now.Add(-20*time.Second), "finalized")
	source := &fakeChain{
		id: new(big.Int).SetUint64(chainID), latest: testHeader(105, now.Add(-31*time.Second), "latest"), finalized: finalized,
	}
	transaction := &fakeChain{id: new(big.Int).SetUint64(chainID), headers: map[uint64]*types.Header{100: finalized}}
	pins := seedDependencies(source, transaction)
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
		Dependencies: pins,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if observation.Healthy || observation.Reason != "latest_stale" {
		t.Fatalf("expected stale latest, got %#v", observation)
	}
}

func TestProberFailsClosedOnUSDGImplementationUpgrade(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	finalized := testHeader(100, now.Add(-20*time.Second), "finalized")
	source := &fakeChain{
		id: new(big.Int).SetUint64(chainID), latest: testHeader(105, now.Add(-2*time.Second), "latest"),
		finalized: finalized,
	}
	transaction := &fakeChain{
		id: new(big.Int).SetUint64(chainID), headers: map[uint64]*types.Header{100: finalized},
	}
	pins := seedDependencies(source, transaction)
	upgraded := common.HexToAddress("0x4000000000000000000000000000000000000004")
	source.storage[mainnetUSDG][implementationSlot] = addressWord(upgraded)
	transaction.storage[mainnetUSDG][implementationSlot] = addressWord(upgraded)
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
		Dependencies: pins,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if observation.Healthy || observation.Reason != "usdg_implementation_mismatch" {
		t.Fatalf("expected USDG implementation mismatch, got %#v", observation)
	}
}

func TestProberFailsClosedOnUSDGImplementationUpgradeAtLatest(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	finalized := testHeader(100, now.Add(-20*time.Second), "finalized")
	latest := testHeader(105, now.Add(-2*time.Second), "latest")
	source := &fakeChain{id: new(big.Int).SetUint64(chainID), latest: latest, finalized: finalized}
	transaction := &fakeChain{
		id:      new(big.Int).SetUint64(chainID),
		headers: map[uint64]*types.Header{100: finalized},
	}
	pins := seedDependencies(source, transaction)
	upgraded := addressWord(common.HexToAddress("0x4000000000000000000000000000000000000004"))
	for _, chain := range []*fakeChain{source, transaction} {
		chain.storageAt = map[uint64]map[common.Address]map[common.Hash][]byte{
			105: {mainnetUSDG: {implementationSlot: upgraded}},
		}
	}
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
		Dependencies: pins,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if observation.Healthy || observation.Reason != "usdg_implementation_mismatch" {
		t.Fatalf("expected latest USDG implementation mismatch, got %#v", observation)
	}
}

func TestProberFailsClosedOnAAPLBeaconUpgrade(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	finalized := testHeader(100, now.Add(-20*time.Second), "finalized")
	source := &fakeChain{
		id: new(big.Int).SetUint64(chainID), latest: testHeader(105, now.Add(-2*time.Second), "latest"),
		finalized: finalized,
	}
	transaction := &fakeChain{
		id: new(big.Int).SetUint64(chainID), headers: map[uint64]*types.Header{100: finalized},
	}
	pins := seedDependencies(source, transaction)
	upgraded := common.HexToAddress("0x5000000000000000000000000000000000000005")
	source.calls[pins.AAPLBeacon] = addressWord(upgraded)
	transaction.calls[pins.AAPLBeacon] = addressWord(upgraded)
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
		Dependencies: pins,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if observation.Healthy || observation.Reason != "aapl_implementation_mismatch" {
		t.Fatalf("expected AAPL implementation mismatch, got %#v", observation)
	}
}

func TestProberFailsClosedOnDependencyRPCDisagreement(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	finalized := testHeader(100, now.Add(-20*time.Second), "finalized")
	source := &fakeChain{
		id: new(big.Int).SetUint64(chainID), latest: testHeader(105, now.Add(-2*time.Second), "latest"),
		finalized: finalized,
	}
	transaction := &fakeChain{
		id: new(big.Int).SetUint64(chainID), headers: map[uint64]*types.Header{100: finalized},
	}
	pins := seedDependencies(source, transaction)
	transaction.storage[mainnetUSDG][implementationSlot] = addressWord(
		common.HexToAddress("0x6000000000000000000000000000000000000006"),
	)
	prober := NewProber(source, transaction, Config{
		LatestMaxAge: 30 * time.Second, FinalizedMaxAge: 2 * time.Minute, MaxFinalizedLag: 128,
		Dependencies: pins,
	})
	prober.clock = func() time.Time { return now }

	observation := prober.Probe(context.Background(), HeadState{})
	if observation.Healthy || observation.Reason != "dependency_rpc_disagreement" {
		t.Fatalf("expected dependency RPC disagreement, got %#v", observation)
	}
}

func seedDependencies(chains ...*fakeChain) DependencyPins {
	usdgProxyCode := []byte("usdg-proxy")
	usdgImplementationCode := []byte("usdg-implementation")
	aaplProxyCode := []byte("aapl-proxy")
	aaplBeaconCode := []byte("aapl-beacon")
	aaplImplementationCode := []byte("aapl-implementation")
	pins := DependencyPins{
		USDGProxyCodeHash:          crypto.Keccak256Hash(usdgProxyCode),
		USDGImplementation:         common.HexToAddress("0x1000000000000000000000000000000000000001"),
		USDGImplementationCodeHash: crypto.Keccak256Hash(usdgImplementationCode),
		AAPLProxyCodeHash:          crypto.Keccak256Hash(aaplProxyCode),
		AAPLBeacon:                 common.HexToAddress("0x2000000000000000000000000000000000000002"),
		AAPLBeaconCodeHash:         crypto.Keccak256Hash(aaplBeaconCode),
		AAPLImplementation:         common.HexToAddress("0x3000000000000000000000000000000000000003"),
		AAPLImplementationCodeHash: crypto.Keccak256Hash(aaplImplementationCode),
	}
	var latest, finalized *types.Header
	for _, chain := range chains {
		if chain.latest != nil {
			latest = chain.latest
		}
		if chain.finalized != nil {
			finalized = chain.finalized
		}
		if latest != nil && finalized != nil {
			break
		}
	}
	for _, chain := range chains {
		if chain.latest == nil {
			chain.latest = latest
		}
		if chain.finalized == nil {
			chain.finalized = finalized
		}
		chain.code = map[common.Address][]byte{
			mainnetUSDG:             usdgProxyCode,
			pins.USDGImplementation: usdgImplementationCode,
			mainnetAAPL:             aaplProxyCode,
			pins.AAPLBeacon:         aaplBeaconCode,
			pins.AAPLImplementation: aaplImplementationCode,
		}
		chain.storage = map[common.Address]map[common.Hash][]byte{
			mainnetUSDG: {implementationSlot: addressWord(pins.USDGImplementation)},
			mainnetAAPL: {beaconSlot: addressWord(pins.AAPLBeacon)},
		}
		chain.calls = map[common.Address][]byte{
			pins.AAPLBeacon: addressWord(pins.AAPLImplementation),
		}
	}
	return pins
}

func addressWord(address common.Address) []byte {
	value := make([]byte, common.HashLength)
	copy(value[12:], address.Bytes())
	return value
}

func testHeader(number uint64, timestamp time.Time, marker string) *types.Header {
	return &types.Header{
		Number: new(big.Int).SetUint64(number), Time: uint64(timestamp.Unix()),
		GasLimit: 30_000_000, BaseFee: big.NewInt(1_000_000), Extra: []byte(marker),
	}
}
