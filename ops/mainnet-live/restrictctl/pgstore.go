package restrictctl

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Result struct {
	RequestID        string `json:"requestId"`
	RequestSHA256    string `json:"requestSha256"`
	Scope            Scope  `json:"scope"`
	StrategyVersion  string `json:"strategyVersion,omitempty"`
	ExecutionAccount string `json:"executionAccountId,omitempty"`
	Mode             Mode   `json:"mode"`
	Version          int64  `json:"version"`
	Idempotent       bool   `json:"idempotent"`
}

type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) (*PGStore, error) {
	if pool == nil {
		return nil, errors.New("PostgreSQL pool is required")
	}
	return &PGStore{pool: pool}, nil
}

func (store *PGStore) Apply(ctx context.Context, signed SignedRequest) (Result, error) {
	if err := signed.Verify(); err != nil {
		return Result{}, err
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Result{}, fmt.Errorf("begin restriction transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return Result{}, fmt.Errorf("set restriction lock timeout: %w", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '15s'"); err != nil {
		return Result{}, fmt.Errorf("set restriction transaction limits: %w", err)
	}
	if existing, found, err := findExisting(ctx, tx, signed); err != nil {
		return Result{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("commit idempotent restriction: %w", err)
		}
		return existing, nil
	}
	fromMode, currentVersion, err := lockTarget(ctx, tx, signed.Request)
	if err != nil {
		return Result{}, err
	}
	if currentVersion != signed.Request.ExpectedVersion {
		return Result{}, fmt.Errorf("control version conflict: expected %d, found %d", signed.Request.ExpectedVersion, currentVersion)
	}
	if fromMode != signed.Request.FromMode {
		return Result{}, fmt.Errorf("control mode conflict: expected %s, found %s", signed.Request.FromMode, fromMode)
	}
	if !AllowedTransition(fromMode, signed.Request.TargetMode) {
		return Result{}, fmt.Errorf("control transition %s to %s is not restrictive", fromMode, signed.Request.TargetMode)
	}
	if err := updateTarget(ctx, tx, signed.Request, fromMode); err != nil {
		return Result{}, err
	}
	resultingVersion := currentVersion + 1
	_, err = tx.Exec(ctx, `
INSERT INTO execution_operator_restriction_events
  (request_id, request_sha256, scope, strategy_version, execution_account_id,
   from_mode, to_mode, expected_version, resulting_version, reason,
   evidence_sha256, operator_id, signer_key_id, request_payload,
   signer_public_key, signature)
VALUES
  ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, $7, $8, $9, $10,
   $11, $12, $13, $14::jsonb, $15, $16)`,
		signed.Request.RequestID, signed.SHA256, string(signed.Request.Scope), signed.Request.StrategyVersion,
		signed.Request.ExecutionAccountID, string(fromMode), string(signed.Request.TargetMode),
		signed.Request.ExpectedVersion, resultingVersion, signed.Request.Reason, signed.Request.EvidenceSHA256,
		signed.Request.OperatorID, signed.SignerKeyID, string(signed.Canonical), []byte(signed.PublicKey), signed.Signature)
	if err != nil {
		return Result{}, fmt.Errorf("append restriction event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit restriction: %w", err)
	}
	return resultFor(signed.Request, signed.SHA256, resultingVersion, false), nil
}

func findExisting(ctx context.Context, tx pgx.Tx, signed SignedRequest) (Result, bool, error) {
	var event struct {
		sha256, scope, strategy, account, from, target, evidence, operator, keyID string
		expected, resulting                                                       int64
		payload, publicKey, signature                                             []byte
	}
	err := tx.QueryRow(ctx, `
SELECT request_sha256, scope, COALESCE(strategy_version, ''),
       COALESCE(execution_account_id, ''), from_mode, to_mode, expected_version,
       resulting_version, evidence_sha256, operator_id, signer_key_id,
       request_payload, signer_public_key, signature
FROM execution_operator_restriction_events
WHERE request_id = $1`, signed.Request.RequestID).Scan(
		&event.sha256, &event.scope, &event.strategy, &event.account, &event.from, &event.target,
		&event.expected, &event.resulting, &event.evidence, &event.operator, &event.keyID,
		&event.payload, &event.publicKey, &event.signature)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("read restriction request: %w", err)
	}
	if event.sha256 != signed.SHA256 || event.scope != string(signed.Request.Scope) ||
		event.strategy != signed.Request.StrategyVersion || event.account != signed.Request.ExecutionAccountID ||
		event.from != string(signed.Request.FromMode) || event.target != string(signed.Request.TargetMode) ||
		event.expected != signed.Request.ExpectedVersion || event.resulting != signed.Request.ExpectedVersion+1 ||
		event.evidence != signed.Request.EvidenceSHA256 || event.operator != signed.Request.OperatorID ||
		event.keyID != signed.SignerKeyID || !bytes.Equal(event.publicKey, signed.PublicKey) ||
		!bytes.Equal(event.signature, signed.Signature) {
		return Result{}, false, errors.New("request ID conflicts with an existing restriction")
	}
	stored, err := decodePayload(event.payload)
	if err != nil || stored != signed.parsedPayload {
		return Result{}, false, errors.New("stored restriction payload does not match its request digest")
	}
	digest, err := hex.DecodeString(event.sha256)
	if err != nil || !ed25519.Verify(event.publicKey, digest, event.signature) {
		return Result{}, false, errors.New("stored restriction signature is invalid")
	}
	return resultFor(signed.Request, signed.SHA256, event.resulting, true), true, nil
}

func lockTarget(ctx context.Context, tx pgx.Tx, request Request) (Mode, int64, error) {
	var mode string
	var version int64
	var err error
	switch request.Scope {
	case ScopeGlobal:
		err = tx.QueryRow(ctx, `
SELECT mode, version
FROM execution_control
WHERE singleton
FOR UPDATE`).Scan(&mode, &version)
	case ScopeStrategy:
		err = tx.QueryRow(ctx, `
SELECT mode, version
FROM execution_strategy_control
WHERE strategy_version = $1
FOR UPDATE`, request.StrategyVersion).Scan(&mode, &version)
	case ScopeAccount:
		var strategyVersion string
		err = tx.QueryRow(ctx, `
SELECT account_control.mode, account_control.version, account.strategy_version
FROM execution_account_control account_control
JOIN execution_accounts account USING (execution_account_id)
JOIN execution_account_registrations registration
  ON registration.execution_account_id = account.execution_account_id
 AND registration.agent_id = account.agent_id
 AND registration.strategy_version = account.strategy_version
 AND registration.risk_version = account.risk_version
 AND registration.strategy_manifest_sha256 = account.strategy_manifest_sha256
 AND registration.lighter_account_index = account.lighter_account_index
 AND registration.lighter_api_key_index = account.lighter_api_key_index
 AND registration.robinhood_owner = account.owner_address
 AND registration.robinhood_vault = account.robinhood_vault
 AND registration.robinhood_signer = account.robinhood_signer
 AND registration.binding_sha256 = account.binding_sha256
WHERE account.execution_account_id = $1
FOR UPDATE OF account_control`, request.ExecutionAccountID).Scan(&mode, &version, &strategyVersion)
		if err == nil && strategyVersion != request.StrategyVersion {
			return "", 0, errors.New("execution account is not bound to the requested strategy")
		}
	default:
		return "", 0, errors.New("invalid restriction scope")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, errors.New("restriction target is not registered")
	}
	if err != nil {
		return "", 0, fmt.Errorf("lock restriction target: %w", err)
	}
	return Mode(mode), version, nil
}

func updateTarget(ctx context.Context, tx pgx.Tx, request Request, from Mode) error {
	var command pgconn.CommandTag
	var err error
	switch request.Scope {
	case ScopeGlobal:
		command, err = tx.Exec(ctx, `
UPDATE execution_control
SET mode = $1, reason = $2, version = version + 1, updated_at = now()
WHERE singleton AND mode = $3 AND version = $4`, request.TargetMode, request.Reason, from, request.ExpectedVersion)
	case ScopeStrategy:
		command, err = tx.Exec(ctx, `
UPDATE execution_strategy_control
SET mode = $1, reason = $2, version = version + 1, updated_at = now()
WHERE strategy_version = $3 AND mode = $4 AND version = $5`, request.TargetMode, request.Reason,
			request.StrategyVersion, from, request.ExpectedVersion)
	case ScopeAccount:
		command, err = tx.Exec(ctx, `
UPDATE execution_account_control
SET mode = $1, reason = $2, version = version + 1, updated_at = now()
WHERE execution_account_id = $3 AND mode = $4 AND version = $5`, request.TargetMode, request.Reason,
			request.ExecutionAccountID, from, request.ExpectedVersion)
	}
	if err != nil {
		return fmt.Errorf("restrict execution control: %w", err)
	}
	if command.RowsAffected() != 1 {
		return errors.New("execution control changed concurrently")
	}
	return nil
}

func decodePayload(body []byte) (payload, error) {
	var value payload
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return payload{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return payload{}, errors.New("restriction payload contains trailing data")
	}
	return value, nil
}

func resultFor(request Request, digest string, version int64, idempotent bool) Result {
	return Result{
		RequestID:        request.RequestID,
		RequestSHA256:    digest,
		Scope:            request.Scope,
		StrategyVersion:  request.StrategyVersion,
		ExecutionAccount: request.ExecutionAccountID,
		Mode:             request.TargetMode,
		Version:          version,
		Idempotent:       idempotent,
	}
}
