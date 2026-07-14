package aaplrelay

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type fakeChain struct {
	id       uint64
	header   *types.Header
	code     []byte
	call     func(ethereum.CallMsg, *big.Int) ([]byte, error)
	nonce    uint64
	estimate uint64
	tip      *big.Int
	balance  *big.Int
}

func (chain *fakeChain) ChainID(context.Context) (*big.Int, error) {
	return new(big.Int).SetUint64(chain.id), nil
}

func (chain *fakeChain) HeaderByNumber(context.Context, *big.Int) (*types.Header, error) {
	return chain.header, nil
}

func (chain *fakeChain) CodeAt(context.Context, common.Address, *big.Int) ([]byte, error) {
	return chain.code, nil
}

func (chain *fakeChain) CallContract(_ context.Context, call ethereum.CallMsg, block *big.Int) ([]byte, error) {
	return chain.call(call, block)
}

func (chain *fakeChain) EstimateGas(context.Context, ethereum.CallMsg) (uint64, error) {
	return chain.estimate, nil
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

func (*fakeChain) SendTransaction(context.Context, *types.Transaction) error {
	return errors.New("unused")
}

func (*fakeChain) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	return nil, errors.New("unused")
}

func (*fakeChain) TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error) {
	return nil, false, errors.New("unused")
}

func TestSourceReaderRequiresExactDualRPCConsensus(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	first, config := sourceFixture(t, now, 213_456_789_01, now.Add(-time.Minute))
	second, _ := sourceFixture(t, now, 213_456_789_01, now.Add(-time.Minute))
	reader, err := NewSourceReader(first, second, config)
	if err != nil {
		t.Fatal(err)
	}
	reader.clock = func() time.Time { return now }
	observation, err := reader.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if observation.RoundID.Uint64() != 52 || observation.Answer.Int64() != 213_456_789_01 ||
		observation.UpdatedAt != uint64(now.Add(-time.Minute).Unix()) ||
		observation.AnsweredInRound.Uint64() != 52 {
		t.Fatalf("unexpected observation: %+v", observation)
	}

	second.call = sourceCall(t, 213_456_789_02, now.Add(-time.Minute))
	if _, err := reader.Observe(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "response disagreement") {
		t.Fatalf("expected exact consensus failure, got %v", err)
	}
}

func TestSourceReaderRejectsStaleSourceTimestamp(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	updatedAt := now.Add(-MaxSourceAge - time.Second)
	first, config := sourceFixture(t, now, 213_456_789_01, updatedAt)
	second, _ := sourceFixture(t, now, 213_456_789_01, updatedAt)
	reader, err := NewSourceReader(first, second, config)
	if err != nil {
		t.Fatal(err)
	}
	reader.clock = func() time.Time { return now }
	if _, err := reader.Observe(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "source round is stale") {
		t.Fatalf("expected stale source failure, got %v", err)
	}
}

func TestSourceConflictRejectsRegressionAndMutation(t *testing.T) {
	previous := PriceObservation{
		RoundID: big.NewInt(52), Answer: big.NewInt(100), UpdatedAt: 1000,
		AnsweredInRound: big.NewInt(52), BlockNumber: 100, BlockHash: common.HexToHash("0x01"),
	}
	regressed := previous
	regressed.RoundID = big.NewInt(51)
	regressed.BlockNumber = 101
	if sourceConflict(previous, regressed) != "AAPL source round regressed" {
		t.Fatal("round regression was accepted")
	}
	mutated := previous
	mutated.Answer = big.NewInt(101)
	if sourceConflict(previous, mutated) != "AAPL source round mutated" {
		t.Fatal("same-round mutation was accepted")
	}
}

func TestRelayCalldataPreservesSourceRound(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		TargetFeed:     common.HexToAddress("0x1111111111111111111111111111111111111111"),
		TargetCodeHash: common.HexToHash("0x01"), PrivateKey: key,
	}
	feed, err := NewTargetFeed(config)
	if err != nil {
		t.Fatal(err)
	}
	observation := PriceObservation{
		RoundID: big.NewInt(52), Answer: big.NewInt(21_345_678_901), UpdatedAt: 9_990,
		AnsweredInRound: big.NewInt(53),
	}
	data, err := feed.PackReport(7, observation)
	if err != nil {
		t.Fatal(err)
	}
	method := feed.contract.Methods["report"]
	values, err := method.Inputs.Unpack(data[4:])
	if err != nil {
		t.Fatal(err)
	}
	if values[0].(uint64) != 7 || values[1].(*big.Int).Uint64() != 52 ||
		values[2].(*big.Int).Cmp(observation.Answer) != 0 || values[3].(uint64) != 9_990 ||
		values[4].(*big.Int).Uint64() != 53 {
		t.Fatalf("source fields changed in relay calldata: %#v", values)
	}
}

func TestSigningRequiresExactJournalNonceAndCalldata(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		TargetFeed:     common.HexToAddress("0x1111111111111111111111111111111111111111"),
		TargetCodeHash: common.HexToHash("0x01"), PrivateKey: key,
		SignerAddress: crypto.PubkeyToAddress(key.PublicKey), MaxGasLimit: 180_000,
		MaxPriorityFee: big.NewInt(100_000_000), MaxFeePerGas: big.NewInt(10_000_000_000),
		MaxTransactionCost: big.NewInt(1_800_000_000_000_000),
		MinimumGasReserve:  big.NewInt(2_000_000_000_000_000),
	}
	feed, err := NewTargetFeed(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	target := &fakeChain{
		header: &types.Header{
			Number: big.NewInt(100), Time: uint64(now.Unix()), BaseFee: big.NewInt(1_000_000_000),
		},
		nonce: 1, estimate: 100_000, tip: big.NewInt(10_000_000), balance: big.NewInt(1e18),
	}
	service := &Service{config: config, target: target, feed: feed, clock: func() time.Time { return now }}
	observation := PriceObservation{
		RoundID: big.NewInt(52), Answer: big.NewInt(21_345_678_901),
		UpdatedAt: uint64(now.Add(-time.Minute).Unix()), AnsweredInRound: big.NewInt(52),
	}
	if _, err := service.sign(context.Background(), 1, observation, nil); err == nil || !errors.Is(err, errNonceDrift) {
		t.Fatalf("expected nonce drift, got %v", err)
	}
	target.nonce = 0
	pending, err := service.sign(context.Background(), 1, observation, nil)
	if err != nil {
		t.Fatal(err)
	}
	transaction := new(types.Transaction)
	if err := transaction.UnmarshalBinary(pending.Raw); err != nil {
		t.Fatal(err)
	}
	if err := verifySignedReport(transaction, config, feed, 1, observation); err != nil {
		t.Fatal(err)
	}
	mutated := observation
	mutated.Answer = big.NewInt(21_345_678_902)
	if err := verifySignedReport(transaction, config, feed, 1, mutated); err == nil {
		t.Fatal("signed relay transaction accepted mutated source data")
	}
}

func TestRPCOriginsMustBeIndependent(t *testing.T) {
	if err := validateIndependentRPCs(
		"https://arb-1.example/rpc", "https://arb-2.example/rpc", "https://rh.example/rpc",
	); err != nil {
		t.Fatal(err)
	}
	if err := validateIndependentRPCs(
		"https://arb.example/one", "https://arb.example/two", "https://rh.example/rpc",
	); err == nil {
		t.Fatal("duplicate RPC host was accepted")
	}
}

func sourceFixture(
	t *testing.T,
	now time.Time,
	answer int64,
	updatedAt time.Time,
) (*fakeChain, Config) {
	t.Helper()
	code := []byte{0x60, 0x01, 0x60, 0x00}
	header := &types.Header{
		Number: big.NewInt(100), Time: uint64(now.Add(-10 * time.Second).Unix()),
		GasLimit: 30_000_000, Extra: []byte("finalized"),
	}
	chain := &fakeChain{
		id: SourceChainID, header: header, code: code,
		call: sourceCall(t, answer, updatedAt),
	}
	return chain, Config{
		SourceFeed: common.HexToAddress(SourceFeedHex), SourceCodeHash: crypto.Keccak256Hash(code),
		FinalizedMaxAge: 20 * time.Minute,
	}
}

func sourceCall(t *testing.T, answer int64, updatedAt time.Time) func(ethereum.CallMsg, *big.Int) ([]byte, error) {
	t.Helper()
	contract, err := abi.JSON(bytes.NewBufferString(sourceABIJSON))
	if err != nil {
		t.Fatal(err)
	}
	return func(call ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		for _, method := range contract.Methods {
			if len(call.Data) < 4 || !bytes.Equal(call.Data[:4], method.ID) {
				continue
			}
			switch method.Name {
			case "decimals":
				return method.Outputs.Pack(uint8(SourceDecimals))
			case "description":
				return method.Outputs.Pack("AAPL / USD")
			case "latestRoundData":
				return method.Outputs.Pack(
					big.NewInt(52), big.NewInt(answer), big.NewInt(updatedAt.Unix()-1),
					big.NewInt(updatedAt.Unix()), big.NewInt(52),
				)
			}
		}
		return nil, errors.New("unexpected call")
	}
}
