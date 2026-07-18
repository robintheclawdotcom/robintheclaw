package evaluation

import (
	"strings"
	"testing"
	"time"
)

func TestRobinhoodSourceBlockBinding(t *testing.T) {
	observed := time.Unix(1_700_000_000, 0).UTC()
	snapshot := robinhoodSnapshot{
		FinalizedNumber: 100, FinalizedHash: "0x" + strings.Repeat("a", 64),
		FinalizedTimestamp: 1_699_999_100, SourceBlockNumber: 110,
		SourceBlockHash: "0x" + strings.Repeat("b", 64), SourceBlockTimestamp: 1_700_000_000,
	}
	if !robinhoodSourceBound(snapshot, observed) {
		t.Fatal("ordered source blocks should bind to the authoritative observation time")
	}
	if robinhoodSourceBound(snapshot, observed.Add(time.Millisecond)) {
		t.Fatal("sub-second observation re-stamping must fail")
	}
	snapshot.SourceBlockNumber = 99
	if robinhoodSourceBound(snapshot, observed) {
		t.Fatal("source block before finalized block must fail")
	}
}

func TestVerifySnapshotsRequiresFinalizedRobinhoodEntryAuthority(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	value, state, market := validEntrySnapshots(now)
	if err := verifySnapshots(value, state, market, now); err != nil {
		t.Fatalf("valid entry evidence was rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*robinhoodSnapshot)
	}{
		{
			name: "current global restriction",
			mutate: func(snapshot *robinhoodSnapshot) {
				snapshot.GlobalMode = "REDUCE_ONLY"
			},
		},
		{
			name: "finalized global restriction",
			mutate: func(snapshot *robinhoodSnapshot) {
				snapshot.FinalizedGlobalMode = "REDUCE_ONLY"
			},
		},
		{
			name: "finalized signer substitution",
			mutate: func(snapshot *robinhoodSnapshot) {
				snapshot.FinalizedAgentAddress = "0x0000000000000000000000000000000000000004"
			},
		},
		{
			name: "unfinalized authorization",
			mutate: func(snapshot *robinhoodSnapshot) {
				snapshot.FinalizedAgentEnabled = false
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := value
			test.mutate(&candidate.Robinhood)
			if err := verifySnapshots(candidate, state, market, now); err == nil {
				t.Fatal("unsafe entry evidence was accepted")
			}
		})
	}
}

func validEntrySnapshots(now time.Time) (snapshots, coordinatorState, MarketConfig) {
	owner := "0x0000000000000000000000000000000000000001"
	vault := "0x0000000000000000000000000000000000000002"
	signer := "0x0000000000000000000000000000000000000003"
	state := coordinatorState{
		LighterAccountIndex: 7,
		LighterAPIKeyIndex:  4,
		Owner:               owner,
		Vault:               vault,
		Signer:              signer,
	}
	market := MarketConfig{
		LighterMarketIndex: 101,
		SpotConfigVersion:  1,
		SpotDecimals:       18,
		UIMultiplierE18:    "1000000000000000000",
	}
	value := snapshots{
		Lighter: lighterSnapshot{
			AccountIndex: 7, APIKeyIndex: 4, MarketIndex: 101,
			NonceAligned: true, NoUnknownOrders: true, NoUnknownPositions: true,
			CollateralReady: true, MaintenanceMarginRatio: 2_000_000,
			CollateralMicros: 50_000_000, MaintenanceMarginMicros: 25_000_000, Flat: true,
		},
		Robinhood: robinhoodSnapshot{
			VaultAddress: vault, SignerAddress: signer, OwnerAddress: owner,
			FundingReady: true, WiringVerified: true, FinalityHealthy: true, Flat: true,
			AgentEnabled: true, FinalizedAgentAddress: signer, FinalizedAgentEnabled: true,
			GlobalMode: "ACTIVE", FinalizedGlobalMode: "ACTIVE",
			RiskMode: "ACTIVE", FinalizedRiskMode: "ACTIVE",
			SettlementBalanceRaw: "25000000", NonceAligned: true,
			SpotConfigVersion: 1, StockDecimals: 18,
			UIMultiplierE18: "1000000000000000000", NewUIMultiplierE18: "1000000000000000000",
			OracleHealthy: true, SequencerHealthy: true, SignerGasReady: true,
			FinalizedNumber: 100, FinalizedHash: "0x" + strings.Repeat("a", 64),
			FinalizedTimestamp: uint64(now.Add(-time.Second).Unix()),
			SourceBlockNumber:  110, SourceBlockHash: "0x" + strings.Repeat("b", 64),
			SourceBlockTimestamp: uint64(now.Unix()),
		},
		LighterObserved:   now,
		RobinhoodObserved: now,
	}
	return value, state, market
}
