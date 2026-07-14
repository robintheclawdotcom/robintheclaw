package publisher

import (
	"database/sql"
	"testing"
)

func TestCoordinatorPolicyRequiresEveryControlAndReadinessGate(t *testing.T) {
	manifest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	active := coordinatorPolicyState{
		globalMode:       sql.NullString{String: "ACTIVE", Valid: true},
		strategyMode:     sql.NullString{String: "ACTIVE", Valid: true},
		accountMode:      sql.NullString{String: "ACTIVE", Valid: true},
		strategyManifest: sql.NullString{String: manifest, Valid: true},
		accountManifest:  sql.NullString{String: manifest, Valid: true},
		venueApproved:    sql.NullBool{Bool: true, Valid: true},
		oracleHealthy:    sql.NullBool{Bool: true, Valid: true},
		sequencerHealthy: sql.NullBool{Bool: true, Valid: true},
		reconciled:       sql.NullBool{Bool: true, Valid: true},
		exitReady:        sql.NullBool{Bool: true, Valid: true},
		alertingReady:    sql.NullBool{Bool: true, Valid: true},
		rotationReady:    sql.NullBool{Bool: true, Valid: true},
		readinessFresh:   sql.NullBool{Bool: true, Valid: true},
		lighterMarketID:  sql.NullInt64{Int64: 101, Valid: true},
	}
	if !active.Active(manifest, 101) {
		t.Fatal("fully active coordinator policy was rejected")
	}
	controlFalse := active
	controlFalse.strategyMode.String = "REDUCE_ONLY"
	if controlFalse.Active(manifest, 101) {
		t.Fatal("non-active strategy control enabled policy")
	}
	launchReady := active
	launchReady.accountMode.String = "REDUCE_ONLY"
	if !launchReady.Active(manifest, 101) {
		t.Fatal("launch-ready account control was rejected")
	}
	halted := active
	halted.accountMode.String = "HALTED"
	if halted.Active(manifest, 101) {
		t.Fatal("halted account control enabled policy")
	}
	gateFalse := active
	gateFalse.oracleHealthy.Bool = false
	if gateFalse.Active(manifest, 101) {
		t.Fatal("false readiness gate enabled policy")
	}
	missing := active
	missing.accountMode.Valid = false
	if missing.Active(manifest, 101) {
		t.Fatal("missing account control enabled policy")
	}
	if active.Active(manifest, 102) {
		t.Fatal("mismatched canonical Lighter market enabled policy")
	}
	missingMarket := active
	missingMarket.lighterMarketID.Valid = false
	if missingMarket.Active(manifest, 101) {
		t.Fatal("missing canonical Lighter market enabled policy")
	}
}

func TestMissingSignerJournalBootstrapsOnlyAtOnchainNonceZero(t *testing.T) {
	accounts := []registeredAccount{{id: "account-00000001"}}
	bindings := map[string]RobinhoodBinding{
		"account-00000001": {Signer: "0x1111111111111111111111111111111111111111", Vault: "0x2222222222222222222222222222222222222222"},
	}
	bootstrapSignerJournals(accounts, bindings, map[string]struct{}{})
	binding := bindings["account-00000001"]
	if !signerNonceAligned(binding, 0) {
		t.Fatal("unused signer did not bootstrap at nonce zero")
	}
	if signerNonceAligned(binding, 1) {
		t.Fatal("used signer bootstrapped without a journal")
	}
}

func TestSignerJournalStateRejectsMismatchedAndDuplicateBindings(t *testing.T) {
	bindings := map[string]RobinhoodBinding{"account-00000001": {}}
	identities := map[string]string{"signer:vault": "account-00000001"}
	seen := map[string]struct{}{}
	if err := applySignerJournalState(bindings, identities, seen, signerJournalState{
		signer: "other", vault: "vault", ready: true,
	}); err == nil {
		t.Fatal("mismatched signer journal identity was accepted")
	}
	state := signerJournalState{signer: "signer", vault: "vault", next: 3, ready: true}
	if err := applySignerJournalState(bindings, identities, seen, state); err != nil {
		t.Fatalf("valid signer journal state was rejected: %v", err)
	}
	if err := applySignerJournalState(bindings, identities, seen, state); err == nil {
		t.Fatal("duplicate signer journal state was accepted")
	}
}
