package restrictctl

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPGStoreMonotonicIdempotentRestriction(t *testing.T) {
	databaseURL := os.Getenv("RESTRICTCTL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("RESTRICTCTL_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "restrictctl_" + hex.EncodeToString(random)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, pgTestPrerequisites); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "coordinator", "migrations", "0011_operator_restrictions.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	store, err := NewPGStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	request := validAccountRequest()
	wrongStrategy := request
	wrongStrategy.RequestID = "ops-account-wrong-strategy"
	wrongStrategy.StrategyVersion = "basis-other-v1"
	wrongStrategySigned, err := Sign(wrongStrategy, privateKey, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(ctx, wrongStrategySigned); err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Fatalf("expected strategy binding failure, got %v", err)
	}
	unregistered := request
	unregistered.RequestID = "ops-account-unregistered"
	unregistered.ExecutionAccountID = "account-99999999"
	unregisteredSigned, err := Sign(unregistered, privateKey, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(ctx, unregisteredSigned); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("expected registration failure, got %v", err)
	}
	signed, err := Sign(request, privateKey, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Apply(ctx, signed)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != ModeReduceOnly || result.Version != 1 || result.Idempotent {
		t.Fatalf("unexpected restriction result: %+v", result)
	}
	result, err = store.Apply(ctx, signed)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Idempotent || result.Version != 1 {
		t.Fatalf("unexpected idempotent result: %+v", result)
	}
	conflict := request
	conflict.Reason = "different restriction under reused request identity"
	conflictSigned, err := Sign(conflict, privateKey, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(ctx, conflictSigned); err == nil || !strings.Contains(err.Error(), "request ID conflicts") {
		t.Fatalf("expected request ID conflict, got %v", err)
	}
	stale := request
	stale.RequestID = "ops-account-0002"
	staleSigned, err := Sign(stale, privateKey, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(ctx, staleSigned); err == nil || !strings.Contains(err.Error(), "version conflict") {
		t.Fatalf("expected version conflict, got %v", err)
	}
	halt := request
	halt.RequestID = "ops-account-0003"
	halt.ExpectedVersion = 1
	halt.FromMode = ModeReduceOnly
	halt.TargetMode = ModeHalted
	halt.Reason = "halt account after reconciliation evidence failure"
	haltSigned, err := Sign(halt, privateKey, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	result, err = store.Apply(ctx, haltSigned)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != ModeHalted || result.Version != 2 {
		t.Fatalf("unexpected halt result: %+v", result)
	}
	reverse := request
	reverse.RequestID = "ops-account-0004"
	reverse.ExpectedVersion = 2
	reverse.FromMode = ModeHalted
	reverse.Reason = "attempted reverse transition must fail closed"
	if _, err := Sign(reverse, privateKey, publicKey); err == nil || !strings.Contains(err.Error(), "transition") {
		t.Fatalf("expected reverse transition failure, got %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE execution_operator_restriction_events SET reason = 'mutation' WHERE request_id = $1`, request.RequestID); err == nil {
		t.Fatal("append-only restriction event was mutated")
	}
	var mode string
	var version int64
	if err := pool.QueryRow(ctx, `SELECT mode, version FROM execution_account_control WHERE execution_account_id = $1`, request.ExecutionAccountID).Scan(&mode, &version); err != nil {
		t.Fatal(err)
	}
	if mode != string(ModeHalted) || version != 2 {
		t.Fatalf("unexpected stored control %s v%d", mode, version)
	}
}

const pgTestPrerequisites = `
CREATE OR REPLACE FUNCTION execution_reject_mutation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'append-only table';
END;
$$;

CREATE TABLE execution_accounts (
  execution_account_id TEXT PRIMARY KEY,
  agent_id TEXT NOT NULL,
  strategy_version TEXT NOT NULL,
  risk_version TEXT NOT NULL,
  strategy_manifest_sha256 TEXT NOT NULL,
  lighter_account_index BIGINT NOT NULL,
  lighter_api_key_index SMALLINT NOT NULL,
  owner_address TEXT NOT NULL,
  robinhood_vault TEXT NOT NULL,
  robinhood_signer TEXT NOT NULL,
  binding_sha256 TEXT NOT NULL
);

CREATE TABLE execution_account_registrations (
  execution_account_id TEXT PRIMARY KEY REFERENCES execution_accounts(execution_account_id),
  agent_id TEXT NOT NULL,
  strategy_version TEXT NOT NULL,
  risk_version TEXT NOT NULL,
  strategy_manifest_sha256 TEXT NOT NULL,
  lighter_account_index BIGINT NOT NULL,
  lighter_api_key_index SMALLINT NOT NULL,
  robinhood_owner TEXT NOT NULL,
  robinhood_vault TEXT NOT NULL,
  robinhood_signer TEXT NOT NULL,
  binding_sha256 TEXT NOT NULL
);

CREATE TABLE execution_control (
  singleton BOOLEAN PRIMARY KEY CHECK (singleton),
  mode TEXT NOT NULL,
  reason TEXT NOT NULL,
  version BIGINT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE execution_strategy_control (
  strategy_version TEXT PRIMARY KEY,
  strategy_manifest_sha256 TEXT,
  mode TEXT NOT NULL,
  reason TEXT NOT NULL,
  version BIGINT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE execution_account_control (
  execution_account_id TEXT PRIMARY KEY REFERENCES execution_accounts(execution_account_id),
  mode TEXT NOT NULL,
  reason TEXT NOT NULL,
  version BIGINT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO execution_control (singleton, mode, reason, version)
VALUES (TRUE, 'ACTIVE', 'test setup', 0);
INSERT INTO execution_strategy_control
  (strategy_version, strategy_manifest_sha256, mode, reason, version)
VALUES
  ('basis-aapl-v1', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
   'ACTIVE', 'test setup', 0);
INSERT INTO execution_accounts
  (execution_account_id, agent_id, strategy_version, risk_version, strategy_manifest_sha256,
   lighter_account_index, lighter_api_key_index, owner_address, robinhood_vault,
   robinhood_signer, binding_sha256)
VALUES
  ('account-00000001', 'agent-000000001', 'basis-aapl-v1', 'risk-v1',
   'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 1001, 2,
   '0x1111111111111111111111111111111111111111',
   '0x2222222222222222222222222222222222222222',
   '0x3333333333333333333333333333333333333333',
   'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb');
INSERT INTO execution_account_registrations
  (execution_account_id, agent_id, strategy_version, risk_version, strategy_manifest_sha256,
   lighter_account_index, lighter_api_key_index, robinhood_owner, robinhood_vault,
   robinhood_signer, binding_sha256)
SELECT execution_account_id, agent_id, strategy_version, risk_version, strategy_manifest_sha256,
       lighter_account_index, lighter_api_key_index, owner_address, robinhood_vault,
       robinhood_signer, binding_sha256
FROM execution_accounts;
INSERT INTO execution_account_control (execution_account_id, mode, reason, version)
VALUES ('account-00000001', 'ACTIVE', 'test setup', 0);
`
