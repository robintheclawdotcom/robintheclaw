package publisher

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type changingAccountSource struct {
	mu       sync.Mutex
	versions [][]AccountBinding
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
	return AccountDiscovery{Accounts: append([]AccountBinding(nil), value.versions[index]...)}, nil
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
		AccountIndex: binding.AccountIndex, APIKeyIndex: binding.APIKeyIndex,
		Nonce: 9, ExpectedNonce: 9, CollateralRaw: "100", MaintenanceRequirementRaw: "25",
		MaintenanceMarginRatioMicros: 4_000_000, NoUnknownOrders: true, NoUnknownPositions: true,
		CollateralReady: true, Flat: true, RESTReconstructed: true,
		StateDigest: "lighter", ObservedAt: time.Now().UTC(),
	}, nil
}

type healthyRobinhoodCollector struct{}

func (*healthyRobinhoodCollector) Collect(_ context.Context, binding RobinhoodBinding) (RobinhoodObservation, error) {
	return RobinhoodObservation{
		Vault: binding.Vault, Signer: binding.Signer, Owner: binding.Owner,
		SettlementBalanceRaw: "50000000", OwnerGasRaw: "1", SignerGasRaw: "1",
		AgentEnabled: true, Flat: true, WiringVerified: true, FinalityHealthy: true,
		FundingReady: true, OwnerGasReady: true, SignerGasReady: true,
		RiskMode: "ACTIVE", ObservedAt: time.Now().UTC(),
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

func TestReadinessRequiresBothVenuesFlat(t *testing.T) {
	application := &recordingSnapshotClient{}
	service := &Service{application: application}
	lighter := LighterObservation{
		Nonce: 9, ExpectedNonce: 9, NoUnknownOrders: true, NoUnknownPositions: true,
		Flat: false, RESTReconstructed: true,
	}
	robinhood := RobinhoodObservation{Flat: true, WiringVerified: true, FinalityHealthy: true}

	if err := service.publishReadiness(context.Background(), "account-00000001", true, lighter, robinhood); err != nil {
		t.Fatal(err)
	}
	assertReadinessEvidence(t, application.bodies[0], "reconciled", false)

	lighter.Flat = true
	robinhood.Flat = false
	if err := service.publishReadiness(context.Background(), "account-00000001", true, lighter, robinhood); err != nil {
		t.Fatal(err)
	}
	assertReadinessEvidence(t, application.bodies[1], "reconciled", false)

	robinhood.Flat = true
	if err := service.publishReadiness(context.Background(), "account-00000001", true, lighter, robinhood); err != nil {
		t.Fatal(err)
	}
	assertReadinessEvidence(t, application.bodies[2], "reconciled", true)
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
