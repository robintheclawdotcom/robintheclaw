package publisher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type changingAccountSource struct {
	mu       sync.Mutex
	versions [][]AccountBinding
	rejected []string
	index    int
}

func (value *changingAccountSource) List(context.Context) (AccountDiscovery, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	index := value.index
	if index >= len(value.versions) {
		index = len(value.versions) - 1
	}
	value.index++
	return AccountDiscovery{
		Accounts:    append([]AccountBinding(nil), value.versions[index]...),
		RejectedIDs: append([]string(nil), value.rejected...),
	}, nil
}

func (*changingAccountSource) Close() {}

type recordingLighterCollector struct {
	mu    sync.Mutex
	calls []string
}

func (value *recordingLighterCollector) Collect(_ context.Context, executionID string, binding LighterBinding) (LighterObservation, error) {
	value.mu.Lock()
	value.calls = append(value.calls, executionID)
	value.mu.Unlock()
	return LighterObservation{
		AccountIndex: binding.AccountIndex, APIKeyIndex: binding.APIKeyIndex, MarketID: binding.MarketID,
		Nonce: 9, ExpectedNonce: 9, CollateralRaw: "100", MaintenanceRequirementRaw: "25",
		CollateralMicros: 100_000_000, MaintenanceMarginMicros: 25_000_000,
		MaintenanceMarginRatioMicros: 4_000_000, NoUnknownOrders: true, NoUnknownPositions: true,
		CollateralReady: true, Flat: true, RESTReconstructed: true,
		StateDigest: "lighter", ObservedAt: time.Now().UTC(),
	}, nil
}

type healthyRobinhoodCollector struct{}

func (*healthyRobinhoodCollector) Collect(_ context.Context, binding RobinhoodBinding) (RobinhoodObservation, error) {
	now := time.Now().UTC().Truncate(time.Second)
	return RobinhoodObservation{
		Vault: binding.Vault, Signer: binding.Signer, Owner: binding.Owner,
		SettlementBalanceRaw: "50000000", OwnerGasRaw: "1", SignerGasRaw: "1",
		AgentEnabled: true, FinalizedAgentAddress: binding.Signer, FinalizedAgentEnabled: true, Flat: true,
		WiringVerified: true, FinalityHealthy: true,
		FundingReady: true, OwnerGasReady: true, SignerGasReady: true,
		SignerNonceAligned: true, SpotConfigVersion: 1, StockDecimals: 18,
		UIMultiplierE18: "1000000000000000000", NewUIMultiplierE18: "1000000000000000000",
		OracleHealthy: true, SequencerHealthy: true, GlobalMode: "ACTIVE",
		FinalizedGlobalMode: "ACTIVE", RiskMode: "ACTIVE", FinalizedRiskMode: "ACTIVE",
		FinalizedNumber: 100, FinalizedHash: "0x" + strings.Repeat("a", 64), FinalizedTimestamp: uint64(now.Add(-time.Minute).Unix()),
		SourceBlockNumber: 110, SourceBlockHash: "0x" + strings.Repeat("b", 64), SourceBlockTimestamp: uint64(now.Unix()),
		ObservedAt: now,
	}, nil
}

type recordingSnapshotClient struct {
	mu     sync.Mutex
	calls  int
	bodies [][]byte
}

func (value *recordingSnapshotClient) Post(_ context.Context, _ string, body []byte) error {
	value.mu.Lock()
	defer value.mu.Unlock()
	value.calls++
	value.bodies = append(value.bodies, append([]byte(nil), body...))
	return nil
}

func TestRunOnceDiscoversAndRemovesAccountsWithoutRestart(t *testing.T) {
	first := configTestAccount("10000000-0000-4000-8000-000000000001", 77, "0x1111111111111111111111111111111111111111")
	second := configTestAccount("20000000-0000-4000-8000-000000000002", 78, "0x2222222222222222222222222222222222222222")
	second.PolicyActive = true
	source := &changingAccountSource{versions: [][]AccountBinding{{first}, {first, second}, {second}}}
	lighter := &recordingLighterCollector{}
	coordinator := &recordingSnapshotClient{}
	application := &recordingSnapshotClient{}
	service := &Service{
		config: Config{Enabled: true}, accounts: source, lighter: lighter, robinhood: &healthyRobinhoodCollector{},
		coordinator: coordinator, application: application, session: "test", sequences: make(map[string]int64),
		metrics: newPublisherMetrics("test"),
	}
	for index := 0; index < 3; index++ {
		if err := service.RunOnce(context.Background()); err != nil {
			t.Fatalf("cycle %d: %v", index+1, err)
		}
	}
	want := []string{first.ExecutionAccountID, first.ExecutionAccountID, second.ExecutionAccountID, second.ExecutionAccountID}
	if len(lighter.calls) != len(want) {
		t.Fatalf("collection calls = %v", lighter.calls)
	}
	for index := range want {
		if lighter.calls[index] != want[index] {
			t.Fatalf("collection calls = %v", lighter.calls)
		}
	}
	if coordinator.calls != 8 || application.calls != 4 {
		t.Fatalf("coordinator calls=%d application calls=%d", coordinator.calls, application.calls)
	}
	assertPolicyEvidence(t, application.bodies[0], false)
	assertPolicyEvidence(t, application.bodies[2], true)
}

func TestEmptyDiscoveryIsProcessReadyButRejectedBindingsAreNot(t *testing.T) {
	service := &Service{
		config:   Config{Enabled: true, PollInterval: 5 * time.Second},
		accounts: &changingAccountSource{versions: [][]AccountBinding{{}}},
		metrics:  newPublisherMetrics("test"),
	}
	if err := service.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	service.HealthHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ready"`) {
		t.Fatalf("readyz status=%d body=%s", response.Code, response.Body.String())
	}

	service.accounts = &changingAccountSource{
		versions: [][]AccountBinding{{}},
		rejected: []string{"invalid-account-binding"},
	}
	if err := service.RunOnce(context.Background()); err == nil {
		t.Fatal("rejected empty discovery was accepted")
	}
	response = httptest.NewRecorder()
	service.HealthHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestReadinessTracksKnownStateWhilePositionIsOpen(t *testing.T) {
	application := &recordingSnapshotClient{}
	service := &Service{application: application}
	lighter := LighterObservation{
		Nonce: 9, ExpectedNonce: 9, NoUnknownOrders: true, NoUnknownPositions: true,
		Flat: false, RESTReconstructed: true,
	}
	robinhood := RobinhoodObservation{Flat: true, WiringVerified: true, FinalityHealthy: true, SignerNonceAligned: true}

	if err := service.publishReadiness(context.Background(), "account-00000001", true, lighter, robinhood); err != nil {
		t.Fatal(err)
	}
	assertReadinessEvidence(t, application.bodies[0], "reconciled", true)

	lighter.Flat = true
	robinhood.Flat = false
	if err := service.publishReadiness(context.Background(), "account-00000001", true, lighter, robinhood); err != nil {
		t.Fatal(err)
	}
	assertReadinessEvidence(t, application.bodies[1], "reconciled", true)

	robinhood.Flat = true
	if err := service.publishReadiness(context.Background(), "account-00000001", true, lighter, robinhood); err != nil {
		t.Fatal(err)
	}
	assertReadinessEvidence(t, application.bodies[2], "reconciled", true)

	lighter.NoUnknownPositions = false
	if err := service.publishReadiness(context.Background(), "account-00000001", true, lighter, robinhood); err != nil {
		t.Fatal(err)
	}
	assertReadinessEvidence(t, application.bodies[3], "reconciled", false)
}

func TestPublishAccountPreservesAuthoritativeSourceTTL(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	lighterObserved := now.Add(-2 * time.Second)
	robinhoodObserved := now.Add(-time.Second)
	coordinator := &recordingSnapshotClient{}
	application := &recordingSnapshotClient{}
	service := &Service{
		coordinator: coordinator, application: application, session: "test",
		sequences: make(map[string]int64),
	}
	account := configTestAccount("10000000-0000-4000-8000-000000000001", 77, "0x1111111111111111111111111111111111111111")
	account.ReadinessAccountID = account.ExecutionAccountID
	lighter := LighterObservation{
		Nonce: 9, ExpectedNonce: 9, NoUnknownOrders: true, NoUnknownPositions: true,
		CollateralReady: true, RESTReconstructed: true, ObservedAt: lighterObserved,
	}
	robinhood := RobinhoodObservation{
		WiringVerified: true, FinalityHealthy: true, FundingReady: true,
		OwnerGasReady: true, SignerGasReady: true, SignerNonceAligned: true,
		FinalizedAgentAddress: account.Robinhood.Signer, FinalizedAgentEnabled: true,
		GlobalMode: "ACTIVE", FinalizedGlobalMode: "ACTIVE",
		RiskMode: "ACTIVE", FinalizedRiskMode: "ACTIVE",
		FinalizedNumber: 100, FinalizedHash: "0x" + strings.Repeat("a", 64),
		FinalizedTimestamp: uint64(now.Add(-15 * time.Minute).Unix()),
		SourceBlockNumber:  110, SourceBlockHash: "0x" + strings.Repeat("b", 64),
		SourceBlockTimestamp: uint64(robinhoodObserved.Unix()),
		ObservedAt:           robinhoodObserved,
	}
	if err := service.publishAccount(context.Background(), account, lighter, robinhood); err != nil {
		t.Fatal(err)
	}

	if len(coordinator.bodies) != 2 {
		t.Fatalf("coordinator snapshots = %d", len(coordinator.bodies))
	}
	var first CoordinatorSnapshot
	if err := json.Unmarshal(coordinator.bodies[0], &first); err != nil {
		t.Fatal(err)
	}
	if first.Source != "robinhood-chain" {
		t.Fatalf("Robinhood v3 snapshot must bootstrap before dependent sources: %s", first.Source)
	}
	for _, body := range coordinator.bodies {
		var snapshot CoordinatorSnapshot
		if err := json.Unmarshal(body, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.ExpiresAtMS-snapshot.ObservedAtMS != maxEvidenceAge.Milliseconds() {
			t.Fatalf("%s snapshot TTL was re-stamped: %+v", snapshot.Source, snapshot)
		}
		if snapshot.ExpiresAtMS >= snapshot.ReceivedAtMS+maxEvidenceAge.Milliseconds() {
			t.Fatalf("%s snapshot expiry was extended from receipt time: %+v", snapshot.Source, snapshot)
		}
		if snapshot.Source == "robinhood-chain" {
			encoded, err := json.Marshal(snapshot.Payload)
			if err != nil {
				t.Fatal(err)
			}
			var payload RobinhoodPayload
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.FinalizedNumber != robinhood.FinalizedNumber ||
				payload.FinalizedHash != robinhood.FinalizedHash ||
				payload.FinalizedTimestamp != robinhood.FinalizedTimestamp ||
				payload.FinalizedAgentAddress != robinhood.FinalizedAgentAddress ||
				payload.FinalizedAgentEnabled != robinhood.FinalizedAgentEnabled ||
				payload.FinalizedAgentRevoked != robinhood.FinalizedAgentRevoked ||
				payload.GlobalMode != robinhood.GlobalMode ||
				payload.FinalizedGlobalMode != robinhood.FinalizedGlobalMode ||
				payload.FinalizedRiskMode != robinhood.FinalizedRiskMode ||
				payload.SourceBlockNumber != robinhood.SourceBlockNumber ||
				payload.SourceBlockHash != robinhood.SourceBlockHash ||
				payload.SourceBlockTimestamp != robinhood.SourceBlockTimestamp {
				t.Fatalf("Robinhood source block evidence was not persisted: %+v", payload)
			}
		}
	}

	var readiness ReadinessSnapshot
	if err := json.Unmarshal(application.bodies[0], &readiness); err != nil {
		t.Fatal(err)
	}
	for _, evidence := range readiness.Evidence {
		switch evidence.CheckName {
		case "reconciled":
			if !evidence.ObservedAt.Equal(lighterObserved) || !evidence.ExpiresAt.Equal(lighterObserved.Add(maxEvidenceAge)) {
				t.Fatalf("reconciled evidence did not preserve the oldest source time: %+v", evidence)
			}
		case "robinhood_funded":
			if !evidence.ObservedAt.Equal(robinhoodObserved) || !evidence.ExpiresAt.Equal(robinhoodObserved.Add(maxEvidenceAge)) {
				t.Fatalf("Robinhood readiness evidence was re-stamped: %+v", evidence)
			}
		}
	}
}

func assertPolicyEvidence(t *testing.T, body []byte, expected bool) {
	t.Helper()
	var snapshot ReadinessSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatal(err)
	}
	for _, evidence := range snapshot.Evidence {
		if evidence.CheckName == "policy_active" {
			if evidence.Ready != expected || evidence.Source != "coordinator-account-policy" {
				t.Fatalf("policy evidence = %+v", evidence)
			}
			return
		}
	}
	t.Fatal("policy_active evidence missing")
}

func assertReadinessEvidence(t *testing.T, body []byte, name string, expected bool) {
	t.Helper()
	var snapshot ReadinessSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatal(err)
	}
	for _, evidence := range snapshot.Evidence {
		if evidence.CheckName == name {
			if evidence.Ready != expected {
				t.Fatalf("%s evidence = %+v", name, evidence)
			}
			return
		}
	}
	t.Fatalf("%s evidence missing", name)
}
