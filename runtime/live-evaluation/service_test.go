package evaluation

import (
	"context"
	"testing"
	"time"
)

type researchStub struct {
	candidates []PaperCandidate
	exits      []PaperExit
}

func (stub researchStub) Candidates(context.Context, time.Time) ([]PaperCandidate, error) {
	return stub.candidates, nil
}

func (stub researchStub) Exits(context.Context, time.Time) ([]PaperExit, error) {
	return stub.exits, nil
}

type productStub struct{ accounts []ProductAccount }

func (stub productStub) Accounts(context.Context, time.Time) ([]ProductAccount, error) {
	return stub.accounts, nil
}

type storeStub struct{ seen map[string]bool }

func (stub *storeStub) Approve(_ context.Context, candidate PaperCandidate, account ProductAccount,
	_ time.Time, _ time.Duration, _ uint64, _ uint32) (bool, error) {
	key := candidate.EvaluationID + "/" + account.ExecutionAccountID
	if stub.seen[key] {
		return false, nil
	}
	stub.seen[key] = true
	return true, nil
}

func (stub *storeStub) ApproveExit(_ context.Context, exit PaperExit, account ProductAccount,
	_ time.Time, _ time.Duration, _ uint32) (bool, error) {
	key := exit.EvaluationID + "/" + account.ExecutionAccountID
	if stub.seen[key] {
		return false, nil
	}
	stub.seen[key] = true
	return true, nil
}

func TestServiceApprovalIsIdempotent(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	store := &storeStub{seen: make(map[string]bool)}
	service, err := NewService(researchStub{candidates: []PaperCandidate{validCandidate(now)}},
		productStub{[]ProductAccount{validProduct(now)}}, store, Config{
			Enabled: true, PollInterval: time.Second, ApprovalLifetime: 4 * time.Second,
			MinimumNetEdgePPM: 2_000, LighterMarket: 101,
		})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	approved, err := service.RunOnce(context.Background())
	if err != nil || approved != 1 {
		t.Fatalf("first run approved %d: %v", approved, err)
	}
	approved, err = service.RunOnce(context.Background())
	if err != nil || approved != 0 {
		t.Fatalf("second run approved %d: %v", approved, err)
	}
}

func TestServiceApprovesNaturalExitOnce(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	store := &storeStub{seen: make(map[string]bool)}
	exit := validExit(now)
	service, err := NewService(researchStub{exits: []PaperExit{exit}},
		productStub{[]ProductAccount{validProduct(now)}}, store, Config{
			Enabled: true, PollInterval: time.Second, ApprovalLifetime: 4 * time.Second,
			MinimumNetEdgePPM: 2_000, LighterMarket: 101,
		})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	for run, want := range []int{1, 0} {
		approved, err := service.RunOnce(context.Background())
		if err != nil || approved != want {
			t.Fatalf("run %d approved %d: %v", run, approved, err)
		}
	}
}
