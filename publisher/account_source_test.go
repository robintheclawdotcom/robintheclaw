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
