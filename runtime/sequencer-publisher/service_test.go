package sequencerpublisher

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestContinuousHealthRetainsStartedAtOnlyWithoutGap(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	state := PublisherState{
		ObservedHealthy: true, ContinuousStartedAt: uint64(now.Add(-time.Hour).Unix()),
		ObservedAt: now.Add(-15 * time.Second),
	}
	if actual := nextStartedAt(state, true, now, 45*time.Second); actual != state.ContinuousStartedAt {
		t.Fatalf("startedAt changed during continuous health: %d", actual)
	}
	if actual := nextStartedAt(state, false, now, 45*time.Second); actual != uint64(now.Unix()) {
		t.Fatalf("unhealthy transition did not reset startedAt: %d", actual)
	}
	state.ObservedAt = now.Add(-46 * time.Second)
	if actual := nextStartedAt(state, true, now, 45*time.Second); actual != uint64(now.Unix()) {
		t.Fatalf("observation gap did not reset startedAt: %d", actual)
	}
}

func TestSignedReportIdentityAndBounds(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	feed, err := NewFeed(common.HexToAddress("0x1000000000000000000000000000000000000001"), common.HexToHash("0x01"))
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		FeedAddress: feed.address, PrivateKey: key, SignerAddress: crypto.PubkeyToAddress(key.PublicKey),
		MaxGasLimit: 150_000, MaxPriorityFee: big.NewInt(100_000_000),
		MaxFeePerGas: big.NewInt(10_000_000_000), MaxTransactionCost: big.NewInt(1_500_000_000_000_000),
	}
	data, err := feed.PackReport(7, true, 1_999_999_000)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID: new(big.Int).SetUint64(chainID), Nonce: 3, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2),
		Gas: 100_000, To: &feed.address, Value: new(big.Int), Data: data,
	})
	signed, err := types.SignTx(unsigned, types.LatestSignerForChainID(new(big.Int).SetUint64(chainID)), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySignedReport(signed, config, feed, 7, true, 1_999_999_000); err != nil {
		t.Fatalf("valid transaction rejected: %v", err)
	}
	if err := verifySignedReport(signed, config, feed, 8, true, 1_999_999_000); err == nil {
		t.Fatal("calldata substitution was accepted")
	}
	otherKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	other, err := types.SignTx(unsigned, types.LatestSignerForChainID(new(big.Int).SetUint64(chainID)), otherKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySignedReport(other, config, feed, 7, true, 1_999_999_000); err == nil {
		t.Fatal("signer substitution was accepted")
	}
}
