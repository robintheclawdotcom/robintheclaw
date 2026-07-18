package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errNotFound        = errors.New("binding not found")
	errBindingConflict = errors.New("execution account binding conflict")
)

//go:embed migrations/*.sql
var migrations embed.FS

type bindingStore interface {
	Create(context.Context, binding) (binding, error)
	Get(context.Context, string) (binding, error)
	MarkConfirming(context.Context, binding, string) (binding, error)
	MarkDeployed(context.Context, binding, uint64) (binding, error)
	MarkAuthorized(context.Context, binding, string, uint64) (binding, error)
	Block(context.Context, binding, string) error
	ClaimNonce(context.Context, string, string, time.Time) (bool, error)
}

func (value *pgStore) MarkDeployed(ctx context.Context, record binding, block uint64) (binding, error) {
	if block == 0 {
		return binding{}, errBindingConflict
	}
	tx, err := value.pool.Begin(ctx)
	if err != nil {
		return binding{}, errors.New("begin deployment finalization")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
        UPDATE robinhood_execution_bindings
        SET deployment_block = $3, updated_at = now()
        WHERE execution_account_id = $1 AND deployment_tx_hash = $2 AND status = 'confirming'
          AND (deployment_block IS NULL OR deployment_block = $3)`,
		record.ExecutionAccountID, record.DeploymentTxHash, block)
	if err != nil || command.RowsAffected() != 1 {
		return binding{}, errBindingConflict
	}
	if record.DeploymentBlock == 0 {
		if err := appendAudit(ctx, tx, record, "deployment_finalized", map[string]any{
			"txHash": record.DeploymentTxHash,
			"block":  block,
		}); err != nil {
			return binding{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return binding{}, errors.New("commit deployment finalization")
	}
	return value.Get(ctx, record.ExecutionAccountID)
}

type pgStore struct {
	pool *pgxpool.Pool
}

func openStore(ctx context.Context, databaseURL string, runMigrations bool) (*pgStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, errors.New("open Robinhood provisioner database")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("connect Robinhood provisioner database")
	}
	if runMigrations {
		contents, err := migrations.ReadFile("migrations/0001_bindings.sql")
		if err != nil {
			pool.Close()
			return nil, errors.New("read Robinhood provisioner migration")
		}
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			pool.Close()
			return nil, fmt.Errorf("apply Robinhood provisioner migration: %w", err)
		}
	}
	return &pgStore{pool: pool}, nil
}

func (value *pgStore) Close() { value.pool.Close() }

func (value *pgStore) Create(ctx context.Context, record binding) (binding, error) {
	tx, err := value.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return binding{}, errors.New("begin binding creation")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `
		INSERT INTO robinhood_execution_bindings (
			execution_account_id, owner_address, kms_key_id, signer_address, key_version,
			factory_address, registry_address, policy_digest, factory_code_hash, registry_code_hash,
			vault_code_hash, risk_manager_code_hash, spot_adapter_code_hash,
			vault_address, risk_manager_address, spot_adapter_address, status,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
			'awaiting_deployment', $17, $17
		) ON CONFLICT (execution_account_id) DO NOTHING`,
		record.ExecutionAccountID, record.OwnerAddress, record.KMSKeyID, record.SignerAddress,
		record.KeyVersion, record.FactoryAddress, record.RegistryAddress, record.PolicyDigest,
		record.FactoryCodeHash, record.RegistryCodeHash, record.VaultCodeHash, record.RiskCodeHash,
		record.AdapterCodeHash, record.Graph.Vault, record.Graph.RiskManager,
		record.Graph.SpotAdapter, now)
	if err != nil {
		return binding{}, errors.New("create execution binding")
	}
	stored, err := scanBinding(tx.QueryRow(ctx, bindingSelect+` WHERE execution_account_id = $1`, record.ExecutionAccountID))
	if err != nil {
		return binding{}, errors.New("read execution binding")
	}
	if !sameImmutableBinding(record, stored) {
		return binding{}, errBindingConflict
	}
	if err := appendAudit(ctx, tx, stored, "binding_created", map[string]any{
		"keyVersion": stored.KeyVersion,
		"vault":      stored.Graph.Vault,
	}); err != nil {
		return binding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return binding{}, errors.New("commit execution binding")
	}
	return stored, nil
}

func (value *pgStore) Get(ctx context.Context, executionID string) (binding, error) {
	return scanBinding(value.pool.QueryRow(ctx, bindingSelect+` WHERE execution_account_id = $1`, executionID))
}

func (value *pgStore) MarkConfirming(ctx context.Context, record binding, txHash string) (binding, error) {
	tx, err := value.pool.Begin(ctx)
	if err != nil {
		return binding{}, errors.New("begin deployment confirmation")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		UPDATE robinhood_execution_bindings
		SET status = 'confirming', deployment_tx_hash = $2, updated_at = now()
		WHERE execution_account_id = $1
		  AND (status = 'awaiting_deployment' OR (status = 'confirming' AND deployment_tx_hash = $2))`,
		record.ExecutionAccountID, txHash)
	if err != nil || command.RowsAffected() != 1 {
		return binding{}, errBindingConflict
	}
	record.DeploymentTxHash = txHash
	if err := appendAudit(ctx, tx, record, "deployment_confirmation_started", map[string]any{"txHash": txHash}); err != nil {
		return binding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return binding{}, errors.New("commit deployment confirmation")
	}
	return value.Get(ctx, record.ExecutionAccountID)
}

func (value *pgStore) MarkAuthorized(ctx context.Context, record binding, txHash string, block uint64) (binding, error) {
	if block == 0 || txHash == "" || txHash == record.DeploymentTxHash {
		return binding{}, errBindingConflict
	}
	tx, err := value.pool.Begin(ctx)
	if err != nil {
		return binding{}, errors.New("begin binding activation")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		UPDATE robinhood_execution_bindings
		SET status = 'active', authorization_tx_hash = $3, authorization_block = $4, updated_at = now()
		WHERE execution_account_id = $1 AND deployment_tx_hash = $2 AND status = 'confirming'
		  AND deployment_block IS NOT NULL
		  AND (authorization_tx_hash IS NULL OR authorization_tx_hash = $3)
		  AND (authorization_block IS NULL OR authorization_block = $4)`,
		record.ExecutionAccountID, record.DeploymentTxHash, txHash, block)
	if err != nil || command.RowsAffected() != 1 {
		return binding{}, errBindingConflict
	}
	if err := appendAudit(ctx, tx, record, "agent_authorization_finalized", map[string]any{
		"txHash": txHash,
		"block":  block,
	}); err != nil {
		return binding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return binding{}, errors.New("commit binding activation")
	}
	return value.Get(ctx, record.ExecutionAccountID)
}

func (value *pgStore) Block(ctx context.Context, record binding, reason string) error {
	if len(reason) > 256 {
		reason = reason[:256]
	}
	_, err := value.pool.Exec(ctx, `
		UPDATE robinhood_execution_bindings
		SET status = 'blocked', blocked_reason = $2, updated_at = now()
		WHERE execution_account_id = $1 AND status <> 'active'`, record.ExecutionAccountID, reason)
	return err
}

func (value *pgStore) ClaimNonce(ctx context.Context, caller, nonce string, expires time.Time) (bool, error) {
	tx, err := value.pool.Begin(ctx)
	if err != nil {
		return false, errors.New("begin nonce claim")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM robinhood_provisioner_auth_nonces WHERE expires_at < now()`); err != nil {
		return false, errors.New("expire auth nonces")
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO robinhood_provisioner_auth_nonces (caller_id, nonce, expires_at)
		VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, caller, nonce, expires)
	if err != nil {
		return false, errors.New("claim auth nonce")
	}
	if err := tx.Commit(ctx); err != nil {
		return false, errors.New("commit auth nonce")
	}
	return command.RowsAffected() == 1, nil
}

const bindingSelect = `
	SELECT execution_account_id::text, owner_address, kms_key_id, signer_address,
	       key_version, factory_address, registry_address, policy_digest,
	       factory_code_hash, registry_code_hash, vault_code_hash, risk_manager_code_hash,
	       spot_adapter_code_hash, vault_address, risk_manager_address,
	       spot_adapter_address, COALESCE(deployment_tx_hash, ''),
	       COALESCE(deployment_block, 0), COALESCE(authorization_tx_hash, ''),
	       COALESCE(authorization_block, 0), status, created_at, updated_at
	FROM robinhood_execution_bindings`

type rowScanner interface{ Scan(...any) error }

func scanBinding(row rowScanner) (binding, error) {
	var result binding
	err := row.Scan(
		&result.ExecutionAccountID, &result.OwnerAddress, &result.KMSKeyID,
		&result.SignerAddress, &result.KeyVersion, &result.FactoryAddress,
		&result.RegistryAddress, &result.PolicyDigest, &result.FactoryCodeHash, &result.RegistryCodeHash,
		&result.VaultCodeHash, &result.RiskCodeHash, &result.AdapterCodeHash,
		&result.Graph.Vault, &result.Graph.RiskManager, &result.Graph.SpotAdapter,
		&result.DeploymentTxHash, &result.DeploymentBlock,
		&result.AuthorizationTxHash, &result.AuthorizationBlock, &result.Status,
		&result.CreatedAt, &result.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return binding{}, errNotFound
	}
	if err != nil {
		return binding{}, err
	}
	return result, nil
}

func sameImmutableBinding(left, right binding) bool {
	return left.ExecutionAccountID == right.ExecutionAccountID && left.OwnerAddress == right.OwnerAddress &&
		left.KMSKeyID == right.KMSKeyID && left.SignerAddress == right.SignerAddress &&
		left.KeyVersion == right.KeyVersion && left.FactoryAddress == right.FactoryAddress &&
		left.RegistryAddress == right.RegistryAddress && left.PolicyDigest == right.PolicyDigest &&
		left.FactoryCodeHash == right.FactoryCodeHash && left.RegistryCodeHash == right.RegistryCodeHash && left.VaultCodeHash == right.VaultCodeHash &&
		left.RiskCodeHash == right.RiskCodeHash && left.AdapterCodeHash == right.AdapterCodeHash &&
		left.Graph == right.Graph
}

func appendAudit(ctx context.Context, tx pgx.Tx, record binding, event string, evidence map[string]any) error {
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return errors.New("encode audit evidence")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO robinhood_provisioner_audit (execution_account_id, event_type, evidence)
		VALUES ($1, $2, $3)`, record.ExecutionAccountID, event, string(encoded)); err != nil {
		return errors.New("append provisioner audit")
	}
	return nil
}
