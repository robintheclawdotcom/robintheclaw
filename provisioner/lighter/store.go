package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errNotFound           = errors.New("link not found")
	errBindingMismatch    = errors.New("execution account binding mismatch")
	errRotationOpen       = errors.New("credential provisioning already in progress")
	errAccountBound       = errors.New("Lighter subaccount is already bound to another execution account")
	errBindingRevoked     = errors.New("Lighter credential binding is terminally revoked")
	errNoActiveCredential = errors.New("execution account has no active Lighter credential")
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type credentialStore interface {
	Reserve(context.Context, string, string, int64, uint8, int64) (reservation, error)
	ReserveRevocation(context.Context, string) (revocationReservation, error)
	ReplaceExpiredRevocation(context.Context, credential, time.Time) (revocationReservation, error)
	Complete(context.Context, credential) (credential, error)
	CompleteRevocation(context.Context, credential) (credential, error)
	VerifyRevocationNonce(context.Context, credential, int64) error
	Fail(context.Context, string, string) error
	Latest(context.Context, string) (credential, error)
	Get(context.Context, string, string) (credential, error)
	MarkVerifying(context.Context, credential, string, []byte) (credential, error)
	MarkRevocationVerifying(context.Context, credential, string, []byte) (credential, error)
	Activate(context.Context, credential) (credential, error)
	FinalizeRevocation(context.Context, credential, string) (credential, error)
	Block(context.Context, credential, string) error
	Active(context.Context, string) (credential, error)
	ExpectedNonce(context.Context, credential) (uint64, error)
	ClaimSigningNonce(context.Context, credential, string, uint64, string) (*signedTransaction, error)
	CompleteSigningRequest(context.Context, credential, string, uint64, string, signedTransaction) error
	VerifyActive(context.Context, credential) error
	ClaimAuthNonce(context.Context, string, string, time.Time) (bool, error)
	Audit(context.Context, credential, string, map[string]any) error
	AuditActive(context.Context, credential, string, map[string]any) error
}

type pgStore struct {
	pool *pgxpool.Pool
}

func openStore(ctx context.Context, databaseURL string, runMigrations bool) (*pgStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, errors.New("open provisioner database")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("connect provisioner database")
	}
	value := &pgStore{pool: pool}
	if runMigrations {
		if err := value.migrate(ctx); err != nil {
			pool.Close()
			return nil, err
		}
	}
	return value, nil
}

func (value *pgStore) Close() {
	value.pool.Close()
}

func (value *pgStore) migrate(ctx context.Context) error {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return errors.New("read provisioner migrations")
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read provisioner migration %s: %w", entry.Name(), err)
		}
		if _, err := value.pool.Exec(ctx, string(contents)); err != nil {
			return fmt.Errorf("apply provisioner migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (value *pgStore) Reserve(ctx context.Context, executionID, owner string, accountIndex int64, apiKeyIndex uint8, changeNonce int64) (reservation, error) {
	tx, err := value.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return reservation{}, errors.New("begin credential reservation")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var boundOwner string
	var boundAccount int64
	var boundKey uint8
	var bindingStatus string
	var activeID *string
	err = tx.QueryRow(ctx, `
		SELECT owner_address, account_index, api_key_index, status, active_credential_id::text
		FROM lighter_credential_bindings
		WHERE execution_account_id = $1
		FOR UPDATE`, executionID).Scan(&boundOwner, &boundAccount, &boundKey, &bindingStatus, &activeID)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `
			INSERT INTO lighter_credential_bindings (
				execution_account_id, owner_address, account_index, api_key_index, status
			) VALUES ($1, $2, $3, $4, 'pending')`, executionID, owner, accountIndex, apiKeyIndex)
		if err != nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) && databaseError.Code == "23505" && databaseError.ConstraintName == "lighter_credential_bindings_account_index_key" {
				return reservation{}, errAccountBound
			}
			return reservation{}, errors.New("create credential binding")
		}
		boundOwner, boundAccount, boundKey, bindingStatus = owner, accountIndex, apiKeyIndex, "pending"
	} else if err != nil {
		return reservation{}, errors.New("lock credential binding")
	}
	if bindingStatus == "revoked" {
		return reservation{}, errBindingRevoked
	}
	if bindingStatus == "revocation_pending" || bindingStatus == "revoking" {
		return reservation{}, errRotationOpen
	}
	if boundOwner != owner || boundAccount != accountIndex || boundKey != apiKeyIndex {
		return reservation{}, errBindingMismatch
	}

	open, err := scanCredential(tx.QueryRow(ctx, credentialSelect+`
		WHERE execution_account_id = $1 AND api_key_index = $2
		AND status IN ('generating', 'pending', 'verifying')
		ORDER BY version DESC LIMIT 1`, executionID, apiKeyIndex))
	if err == nil {
		if open.Status != statusGenerating && open.ChangeNonce == changeNonce {
			return reservation{Credential: open, Rotation: activeID != nil && *activeID != "", Existing: true}, nil
		}
		return reservation{}, errRotationOpen
	}
	if !errors.Is(err, errNotFound) {
		return reservation{}, errors.New("check open credential")
	}

	var version int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1
		FROM lighter_credentials
		WHERE execution_account_id = $1`, executionID).Scan(&version); err != nil {
		return reservation{}, errors.New("allocate credential version")
	}
	id, err := newUUID()
	if err != nil {
		return reservation{}, err
	}
	now := time.Now().UTC()
	record := credential{
		ID:                 id,
		ExecutionAccountID: executionID,
		OwnerAddress:       owner,
		AccountIndex:       accountIndex,
		APIKeyIndex:        apiKeyIndex,
		Version:            version,
		Purpose:            purposeAssociation,
		Status:             statusGenerating,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO lighter_credentials (
			id, execution_account_id, owner_address, account_index, api_key_index,
			version, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'generating', $7, $7)`,
		record.ID, executionID, owner, accountIndex, apiKeyIndex, version, now); err != nil {
		return reservation{}, errors.New("reserve credential")
	}
	rotation := activeID != nil && *activeID != ""
	nextBindingStatus := "pending"
	if rotation {
		nextBindingStatus = "rotation_pending"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE lighter_credential_bindings
		SET status = $2, updated_at = $3
		WHERE execution_account_id = $1`, executionID, nextBindingStatus, now); err != nil {
		return reservation{}, errors.New("mark credential binding pending")
	}
	if err := appendAudit(ctx, tx, record, "credential_reserved", map[string]any{"rotation": rotation}); err != nil {
		return reservation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return reservation{}, errors.New("commit credential reservation")
	}
	return reservation{Credential: record, Rotation: rotation}, nil
}

func (value *pgStore) ReserveRevocation(ctx context.Context, executionID string) (revocationReservation, error) {
	tx, err := value.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return revocationReservation{}, errors.New("begin credential revocation")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var owner, bindingStatus string
	var accountIndex int64
	var apiKeyIndex uint8
	var activeID *string
	err = tx.QueryRow(ctx, `
		SELECT owner_address, account_index, api_key_index, status, active_credential_id::text
		FROM lighter_credential_bindings
		WHERE execution_account_id = $1
		FOR UPDATE`, executionID).Scan(&owner, &accountIndex, &apiKeyIndex, &bindingStatus, &activeID)
	if errors.Is(err, pgx.ErrNoRows) || activeID == nil || *activeID == "" {
		return revocationReservation{}, errNoActiveCredential
	}
	if err != nil {
		return revocationReservation{}, errors.New("lock credential revocation binding")
	}
	if bindingStatus == "revoked" {
		return revocationReservation{}, errBindingRevoked
	}

	active, err := scanCredential(tx.QueryRow(ctx, credentialSelect+`
		WHERE c.execution_account_id = $1 AND c.id = $2
		FOR UPDATE`, executionID, *activeID))
	if err != nil || active.Status != statusLinked || active.Purpose != purposeAssociation {
		return revocationReservation{}, errNoActiveCredential
	}

	open, err := scanCredential(tx.QueryRow(ctx, credentialSelect+`
		WHERE c.execution_account_id = $1
		  AND c.status IN ('generating', 'pending', 'verifying')
		ORDER BY c.version DESC LIMIT 1
		FOR UPDATE`, executionID))
	if err == nil {
		if open.Purpose == purposeRevocation && open.ReplacesCredentialID == active.ID &&
			open.Status != statusGenerating &&
			(bindingStatus == "revocation_pending" || bindingStatus == "revoking") {
			return revocationReservation{Active: active, Tombstone: open, Existing: true}, nil
		}
		return revocationReservation{}, errRotationOpen
	}
	if !errors.Is(err, errNotFound) {
		return revocationReservation{}, errors.New("check open credential revocation")
	}
	if bindingStatus != "linked" {
		return revocationReservation{}, errRotationOpen
	}

	var version int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1
		FROM lighter_credentials
		WHERE execution_account_id = $1`, executionID).Scan(&version); err != nil {
		return revocationReservation{}, errors.New("allocate revocation credential version")
	}
	id, err := newUUID()
	if err != nil {
		return revocationReservation{}, err
	}
	now := time.Now().UTC()
	tombstone := credential{
		ID:                   id,
		ExecutionAccountID:   executionID,
		OwnerAddress:         owner,
		AccountIndex:         accountIndex,
		APIKeyIndex:          apiKeyIndex,
		Version:              version,
		Purpose:              purposeRevocation,
		ReplacesCredentialID: active.ID,
		Status:               statusGenerating,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO lighter_credentials (
			id, execution_account_id, owner_address, account_index, api_key_index,
			version, purpose, replaces_credential_id, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'revocation', $7, 'generating', $8, $8)`,
		tombstone.ID, executionID, owner, accountIndex, apiKeyIndex, version, active.ID, now); err != nil {
		return revocationReservation{}, errors.New("reserve revocation credential")
	}
	command, err := tx.Exec(ctx, `
		UPDATE lighter_credential_bindings
		SET status = 'revocation_pending', updated_at = $3
		WHERE execution_account_id = $1 AND status = 'linked' AND active_credential_id = $2`,
		executionID, active.ID, now)
	if err != nil || command.RowsAffected() != 1 {
		return revocationReservation{}, errors.New("mark credential revocation pending")
	}
	if err := appendAudit(ctx, tx, tombstone, "credential_revocation_reserved", map[string]any{
		"replacesCredentialId": active.ID,
	}); err != nil {
		return revocationReservation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return revocationReservation{}, errors.New("commit credential revocation")
	}
	return revocationReservation{Active: active, Tombstone: tombstone}, nil
}

func (value *pgStore) ReplaceExpiredRevocation(ctx context.Context, expired credential, now time.Time) (revocationReservation, error) {
	if expired.Purpose != purposeRevocation || expired.Status != statusPending ||
		expired.ExpiresAtMS > now.UnixMilli() {
		return revocationReservation{}, errors.New("credential revocation is not replaceable")
	}
	tx, err := value.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return revocationReservation{}, errors.New("begin expired credential revocation replacement")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var bindingStatus string
	var activeID *string
	if err := tx.QueryRow(ctx, `
		SELECT status, active_credential_id::text
		FROM lighter_credential_bindings
		WHERE execution_account_id = $1
		FOR UPDATE`, expired.ExecutionAccountID).Scan(&bindingStatus, &activeID); err != nil {
		return revocationReservation{}, errors.New("lock expired credential revocation binding")
	}
	if bindingStatus != "revocation_pending" || activeID == nil ||
		*activeID != expired.ReplacesCredentialID {
		return revocationReservation{}, errors.New("expired credential revocation binding changed")
	}
	canonical, err := scanCredential(tx.QueryRow(ctx, credentialSelect+`
		WHERE c.execution_account_id = $1 AND c.id = $2
		FOR UPDATE`, expired.ExecutionAccountID, expired.ID))
	if err != nil || canonical.Purpose != purposeRevocation ||
		canonical.Status != statusPending || canonical.ExpiresAtMS > now.UnixMilli() ||
		canonical.ReplacesCredentialID != *activeID {
		return revocationReservation{}, errors.New("credential revocation is not replaceable")
	}
	active, err := scanCredential(tx.QueryRow(ctx, credentialSelect+`
		WHERE c.execution_account_id = $1 AND c.id = $2
		FOR UPDATE`, expired.ExecutionAccountID, *activeID))
	if err != nil || active.Status != statusLinked || active.Purpose != purposeAssociation {
		return revocationReservation{}, errNoActiveCredential
	}
	command, err := tx.Exec(ctx, `
		UPDATE lighter_credentials
		SET encrypted_data_key = NULL, cipher_nonce = NULL, ciphertext = NULL,
		    aad_sha256 = NULL, kms_key_id = NULL, status = 'superseded', updated_at = $3
		WHERE execution_account_id = $1 AND id = $2
		  AND purpose = 'revocation' AND status = 'pending'`,
		expired.ExecutionAccountID, expired.ID, now)
	if err != nil || command.RowsAffected() != 1 {
		return revocationReservation{}, errors.New("erase expired revocation credential")
	}

	var version int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1
		FROM lighter_credentials
		WHERE execution_account_id = $1`, expired.ExecutionAccountID).Scan(&version); err != nil {
		return revocationReservation{}, errors.New("allocate replacement revocation version")
	}
	id, err := newUUID()
	if err != nil {
		return revocationReservation{}, err
	}
	replacement := credential{
		ID:                   id,
		ExecutionAccountID:   expired.ExecutionAccountID,
		OwnerAddress:         active.OwnerAddress,
		AccountIndex:         active.AccountIndex,
		APIKeyIndex:          active.APIKeyIndex,
		Version:              version,
		Purpose:              purposeRevocation,
		ReplacesCredentialID: active.ID,
		Status:               statusGenerating,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO lighter_credentials (
			id, execution_account_id, owner_address, account_index, api_key_index,
			version, purpose, replaces_credential_id, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'revocation', $7, 'generating', $8, $8)`,
		replacement.ID, replacement.ExecutionAccountID, replacement.OwnerAddress,
		replacement.AccountIndex, replacement.APIKeyIndex, replacement.Version,
		replacement.ReplacesCredentialID, now); err != nil {
		return revocationReservation{}, errors.New("reserve replacement revocation credential")
	}
	if err := appendAudit(ctx, tx, canonical, "credential_revocation_expired", map[string]any{
		"replacementCredentialId": replacement.ID,
	}); err != nil {
		return revocationReservation{}, err
	}
	if err := appendAudit(ctx, tx, replacement, "credential_revocation_reserved", map[string]any{
		"replacesCredentialId": active.ID,
		"replacesExpiredId":    canonical.ID,
	}); err != nil {
		return revocationReservation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return revocationReservation{}, errors.New("commit replacement revocation credential")
	}
	return revocationReservation{Active: active, Tombstone: replacement}, nil
}

func (value *pgStore) Complete(ctx context.Context, record credential) (credential, error) {
	if record.Purpose != purposeAssociation || record.ReplacesCredentialID != "" {
		return credential{}, errors.New("invalid association credential")
	}
	tx, err := value.pool.Begin(ctx)
	if err != nil {
		return credential{}, errors.New("begin credential storage")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	command, err := tx.Exec(ctx, `
		UPDATE lighter_credentials SET
			public_key = $3, encrypted_data_key = $4, cipher_nonce = $5,
			ciphertext = $6, aad_sha256 = $7, kms_key_id = $8,
			change_nonce = $9, expires_at_ms = $10, tx_type = $11,
			tx_hash = $12, tx_info = $13, message_to_sign = $14,
			status = 'pending', updated_at = $15
		WHERE id = $1 AND execution_account_id = $2
		  AND purpose = 'association' AND status = 'generating'`,
		record.ID, record.ExecutionAccountID, record.PublicKey, record.EncryptedDataKey,
		record.CipherNonce, record.Ciphertext, record.AADDigest, record.KMSKeyID,
		record.ChangeNonce, record.ExpiresAtMS, record.TxType, record.TxHash,
		record.TxInfo, record.MessageToSign, now)
	if err != nil || command.RowsAffected() != 1 {
		return credential{}, errors.New("store generated credential")
	}
	if err := appendAudit(ctx, tx, record, "credential_generated", map[string]any{
		"accountIndex": record.AccountIndex,
		"apiKeyIndex":  record.APIKeyIndex,
		"version":      record.Version,
	}); err != nil {
		return credential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return credential{}, errors.New("commit credential storage")
	}
	return value.Get(ctx, record.ExecutionAccountID, record.ID)
}

func (value *pgStore) CompleteRevocation(ctx context.Context, record credential) (credential, error) {
	if record.Purpose != purposeRevocation || record.ReplacesCredentialID == "" {
		return credential{}, errors.New("invalid revocation credential")
	}
	tx, err := value.pool.Begin(ctx)
	if err != nil {
		return credential{}, errors.New("begin revocation credential storage")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	command, err := tx.Exec(ctx, `
		UPDATE lighter_credentials SET
			public_key = $3, encrypted_data_key = $4, cipher_nonce = $5,
			ciphertext = $6, aad_sha256 = $7, kms_key_id = $8,
			change_nonce = $9, expires_at_ms = $10, tx_type = $11,
			tx_hash = $12, tx_info = $13, message_to_sign = $14,
			status = 'pending', updated_at = $15
		WHERE id = $1 AND execution_account_id = $2
		  AND purpose = 'revocation' AND replaces_credential_id = $16
		  AND status = 'generating'`,
		record.ID, record.ExecutionAccountID, record.PublicKey, record.EncryptedDataKey,
		record.CipherNonce, record.Ciphertext, record.AADDigest, record.KMSKeyID,
		record.ChangeNonce, record.ExpiresAtMS, record.TxType, record.TxHash,
		record.TxInfo, record.MessageToSign, now, record.ReplacesCredentialID)
	if err != nil || command.RowsAffected() != 1 {
		return credential{}, errors.New("store revocation credential")
	}
	if err := appendAudit(ctx, tx, record, "credential_revocation_generated", map[string]any{
		"replacesCredentialId": record.ReplacesCredentialID,
		"transactionHash":      record.TxHash,
	}); err != nil {
		return credential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return credential{}, errors.New("commit revocation credential storage")
	}
	return value.Get(ctx, record.ExecutionAccountID, record.ID)
}

func (value *pgStore) VerifyRevocationNonce(ctx context.Context, active credential, venueNonce int64) error {
	if venueNonce < 0 || venueNonce > maximumLighterNonce {
		return errors.New("invalid Lighter revocation nonce")
	}
	var safe bool
	err := value.pool.QueryRow(ctx, `
		SELECT binding.status = 'revocation_pending'
		   AND binding.active_credential_id = $2
		   AND NOT EXISTS (
		       SELECT 1 FROM lighter_signing_requests
		       WHERE credential_id = $2 AND status = 'claimed'
		   )
		   AND COALESCE((
		       SELECT MAX(nonce) FROM lighter_signing_requests
		       WHERE credential_id = $2 AND status = 'signed'
		   ), -1) < $3
		FROM lighter_credential_bindings AS binding
		WHERE binding.execution_account_id = $1`,
		active.ExecutionAccountID, active.ID, venueNonce).Scan(&safe)
	if err != nil || !safe {
		return errors.New("Lighter signing state is not safe for revocation")
	}
	return nil
}

func (value *pgStore) Fail(ctx context.Context, id, reason string) error {
	return value.blockByID(ctx, id, reason, "credential_generation_failed")
}

func (value *pgStore) Latest(ctx context.Context, executionID string) (credential, error) {
	return scanCredential(value.pool.QueryRow(ctx, credentialSelect+`
		WHERE execution_account_id = $1
		ORDER BY version DESC LIMIT 1`, executionID))
}

func (value *pgStore) Get(ctx context.Context, executionID, id string) (credential, error) {
	return scanCredential(value.pool.QueryRow(ctx, credentialSelect+`
		WHERE execution_account_id = $1 AND id = $2`, executionID, id))
}

func (value *pgStore) MarkVerifying(ctx context.Context, record credential, signature string, txInfo []byte) (credential, error) {
	if record.Purpose != purposeAssociation {
		return credential{}, errors.New("invalid association credential")
	}
	tx, err := value.pool.Begin(ctx)
	if err != nil {
		return credential{}, errors.New("begin credential verification")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	command, err := tx.Exec(ctx, `
		UPDATE lighter_credentials
		SET status = 'verifying', l1_signature = $3, tx_info = $4, updated_at = $5
		WHERE id = $1 AND execution_account_id = $2
		  AND purpose = 'association' AND status = 'pending'`,
		record.ID, record.ExecutionAccountID, signature, txInfo, now)
	if err != nil || command.RowsAffected() != 1 {
		return credential{}, errors.New("mark credential verification")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE lighter_credential_bindings
		SET status = 'verifying', updated_at = $2
		WHERE execution_account_id = $1`, record.ExecutionAccountID, now); err != nil {
		return credential{}, errors.New("mark credential binding verification")
	}
	if err := appendAudit(ctx, tx, record, "association_submission_started", map[string]any{
		"transactionHash": record.TxHash,
	}); err != nil {
		return credential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return credential{}, errors.New("commit credential verification")
	}
	return value.Get(ctx, record.ExecutionAccountID, record.ID)
}

func (value *pgStore) MarkRevocationVerifying(ctx context.Context, record credential, signature string, txInfo []byte) (credential, error) {
	if record.Purpose != purposeRevocation {
		return credential{}, errors.New("invalid revocation credential")
	}
	tx, err := value.pool.Begin(ctx)
	if err != nil {
		return credential{}, errors.New("begin credential revocation verification")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	command, err := tx.Exec(ctx, `
		UPDATE lighter_credentials
		SET status = 'verifying', l1_signature = $3, tx_info = $4, updated_at = $5
		WHERE id = $1 AND execution_account_id = $2
		  AND purpose = 'revocation' AND status = 'pending'`,
		record.ID, record.ExecutionAccountID, signature, txInfo, now)
	if err != nil || command.RowsAffected() != 1 {
		return credential{}, errors.New("mark credential revocation verification")
	}
	command, err = tx.Exec(ctx, `
		UPDATE lighter_credential_bindings
		SET status = 'revoking', updated_at = $3
		WHERE execution_account_id = $1
		  AND active_credential_id = $2 AND status = 'revocation_pending'`,
		record.ExecutionAccountID, record.ReplacesCredentialID, now)
	if err != nil || command.RowsAffected() != 1 {
		return credential{}, errors.New("mark credential binding revoking")
	}
	if err := appendAudit(ctx, tx, record, "credential_revocation_submission_started", map[string]any{
		"transactionHash": record.TxHash,
	}); err != nil {
		return credential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return credential{}, errors.New("commit credential revocation verification")
	}
	return value.Get(ctx, record.ExecutionAccountID, record.ID)
}

func (value *pgStore) Activate(ctx context.Context, record credential) (credential, error) {
	if record.Purpose == purposeRevocation {
		return credential{}, errors.New("revocation credential cannot be activated")
	}
	tx, err := value.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return credential{}, errors.New("begin credential activation")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE lighter_credentials SET status = 'superseded', updated_at = $3
		WHERE execution_account_id = $1 AND id <> $2 AND status = 'linked'`,
		record.ExecutionAccountID, record.ID, now); err != nil {
		return credential{}, errors.New("supersede old credential")
	}
	command, err := tx.Exec(ctx, `
		UPDATE lighter_credentials SET status = 'linked', updated_at = $3
		WHERE execution_account_id = $1 AND id = $2 AND status = 'verifying'`,
		record.ExecutionAccountID, record.ID, now)
	if err != nil || command.RowsAffected() != 1 {
		return credential{}, errors.New("activate credential")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE lighter_credential_bindings
		SET status = 'linked', active_credential_id = $2, updated_at = $3
		WHERE execution_account_id = $1`, record.ExecutionAccountID, record.ID, now); err != nil {
		return credential{}, errors.New("activate credential binding")
	}
	if err := appendAudit(ctx, tx, record, "credential_linked", map[string]any{"version": record.Version}); err != nil {
		return credential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return credential{}, errors.New("commit credential activation")
	}
	return value.Get(ctx, record.ExecutionAccountID, record.ID)
}

func (value *pgStore) FinalizeRevocation(ctx context.Context, record credential, registeredPublicKey string) (credential, error) {
	registeredPublicKey = normalizePublicKey(registeredPublicKey)
	if record.Purpose != purposeRevocation || registeredPublicKey == "" ||
		registeredPublicKey != normalizePublicKey(record.PublicKey) {
		return credential{}, errors.New("registered Lighter key does not prove revocation")
	}
	tx, err := value.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return credential{}, errors.New("begin credential revocation finalization")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var bindingStatus string
	var activeID *string
	if err := tx.QueryRow(ctx, `
		SELECT status, active_credential_id::text
		FROM lighter_credential_bindings
		WHERE execution_account_id = $1
		FOR UPDATE`, record.ExecutionAccountID).Scan(&bindingStatus, &activeID); err != nil {
		return credential{}, errors.New("lock credential revocation binding")
	}
	if bindingStatus != "revoking" || activeID == nil || *activeID != record.ReplacesCredentialID {
		return credential{}, errors.New("credential revocation binding changed")
	}
	tombstone, err := scanCredential(tx.QueryRow(ctx, credentialSelect+`
		WHERE c.execution_account_id = $1 AND c.id = $2
		FOR UPDATE`, record.ExecutionAccountID, record.ID))
	if err != nil || tombstone.Purpose != purposeRevocation ||
		tombstone.Status != statusVerifying ||
		tombstone.ReplacesCredentialID != *activeID ||
		normalizePublicKey(tombstone.PublicKey) != registeredPublicKey {
		return credential{}, errors.New("credential revocation proof mismatch")
	}
	active, err := scanCredential(tx.QueryRow(ctx, credentialSelect+`
		WHERE c.execution_account_id = $1 AND c.id = $2
		FOR UPDATE`, record.ExecutionAccountID, *activeID))
	if err != nil || active.Status != statusLinked ||
		normalizePublicKey(active.PublicKey) == registeredPublicKey {
		return credential{}, errors.New("registered Lighter key did not change")
	}

	now := time.Now().UTC()
	command, err := tx.Exec(ctx, `
		UPDATE lighter_credentials
		SET encrypted_data_key = NULL, cipher_nonce = NULL, ciphertext = NULL,
		    aad_sha256 = NULL, kms_key_id = NULL, status = 'revoked',
		    registered_public_key = CASE WHEN id = $2 THEN $3 ELSE registered_public_key END,
		    updated_at = $4
		WHERE execution_account_id = $1`,
		record.ExecutionAccountID, record.ID, registeredPublicKey, now)
	if err != nil || command.RowsAffected() < 2 {
		return credential{}, errors.New("erase revoked Lighter credentials")
	}
	command, err = tx.Exec(ctx, `
		UPDATE lighter_credential_bindings
		SET status = 'revoked', active_credential_id = NULL, updated_at = $2
		WHERE execution_account_id = $1
		  AND status = 'revoking' AND active_credential_id = $3`,
		record.ExecutionAccountID, now, active.ID)
	if err != nil || command.RowsAffected() != 1 {
		return credential{}, errors.New("finalize credential binding revocation")
	}
	tombstone.RegisteredPublicKey = registeredPublicKey
	if err := appendAudit(ctx, tx, tombstone, "credential_revoked", map[string]any{
		"previousPublicKey":   normalizePublicKey(active.PublicKey),
		"registeredPublicKey": registeredPublicKey,
		"transactionHash":     tombstone.TxHash,
	}); err != nil {
		return credential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return credential{}, errors.New("commit credential revocation finalization")
	}
	return value.Get(ctx, record.ExecutionAccountID, record.ID)
}

func (value *pgStore) Block(ctx context.Context, record credential, reason string) error {
	return value.blockByID(ctx, record.ID, reason, "credential_blocked")
}

func (value *pgStore) blockByID(ctx context.Context, id, reason, event string) error {
	tx, err := value.pool.Begin(ctx)
	if err != nil {
		return errors.New("begin credential block")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	record, err := scanCredential(tx.QueryRow(ctx, credentialSelect+` WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE lighter_credentials SET status = 'blocked', updated_at = $2 WHERE id = $1`, id, now); err != nil {
		return errors.New("block credential")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE lighter_credential_bindings SET status = 'blocked', updated_at = $2
		WHERE execution_account_id = $1`, record.ExecutionAccountID, now); err != nil {
		return errors.New("block credential binding")
	}
	if err := appendAudit(ctx, tx, record, event, map[string]any{"reason": reason}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("commit credential block")
	}
	return nil
}

func (value *pgStore) Active(ctx context.Context, executionID string) (credential, error) {
	return scanCredential(value.pool.QueryRow(ctx, credentialSelect+`
		JOIN lighter_credential_bindings b
		ON b.active_credential_id = c.id
		WHERE c.execution_account_id = $1
		AND c.status = 'linked' AND c.purpose = 'association' AND b.status = 'linked'`, executionID))
}

func (value *pgStore) ExpectedNonce(ctx context.Context, record credential) (uint64, error) {
	if record.ChangeNonce < 0 || record.ChangeNonce >= maximumLighterNonce {
		return 0, errors.New("active credential nonce is invalid")
	}
	var expected int64
	err := value.pool.QueryRow(ctx, `
		SELECT GREATEST($3::bigint, COALESCE(MAX(request.nonce) + 1, $3::bigint))
		FROM lighter_credential_bindings AS binding
		JOIN lighter_credentials AS credential ON credential.id = binding.active_credential_id
		LEFT JOIN lighter_signing_requests AS request ON request.credential_id = credential.id
		WHERE binding.execution_account_id = $1
		  AND binding.status = 'linked'
		  AND credential.id = $2
		  AND credential.status = 'linked'
		GROUP BY binding.execution_account_id`, record.ExecutionAccountID, record.ID, record.ChangeNonce+1).Scan(&expected)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errors.New("execution account credential changed during observation")
	}
	if err != nil || expected < 0 || expected > maximumLighterNonce {
		return 0, errors.New("read expected Lighter nonce")
	}
	return uint64(expected), nil
}

func (value *pgStore) ClaimSigningNonce(ctx context.Context, record credential, intentID string, nonce uint64, requestSHA256 string) (*signedTransaction, error) {
	if record.ChangeNonce < 0 || record.ChangeNonce >= maximumLighterNonce || nonce > uint64(maximumLighterNonce) {
		return nil, errors.New("Lighter signing nonce is out of range")
	}
	tx, err := value.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, errors.New("begin Lighter nonce claim")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var changeNonce int64
	if err := tx.QueryRow(ctx, `
		SELECT credential.change_nonce
		FROM lighter_credential_bindings AS binding
		JOIN lighter_credentials AS credential ON credential.id = binding.active_credential_id
		WHERE binding.execution_account_id = $1
		  AND binding.status = 'linked'
		  AND credential.id = $2
		  AND credential.status = 'linked'
		FOR UPDATE OF binding, credential`, record.ExecutionAccountID, record.ID).Scan(&changeNonce); err != nil ||
		changeNonce < 0 || changeNonce >= maximumLighterNonce {
		return nil, errors.New("execution account credential changed during nonce claim")
	}
	var existingIntent, existingDigest, status string
	var signedJSON []byte
	err = tx.QueryRow(ctx, `
		SELECT intent_id, request_sha256, status, COALESCE(signed_transaction, '{}'::jsonb)::text
		FROM lighter_signing_requests
		WHERE credential_id = $1 AND nonce = $2
		FOR UPDATE`, record.ID, nonce).Scan(&existingIntent, &existingDigest, &status, &signedJSON)
	if err == nil {
		if existingIntent != intentID || existingDigest != requestSHA256 {
			return nil, errors.New("Lighter signing nonce is already bound to another request")
		}
		if status != "signed" {
			return nil, errors.New("Lighter signing request is claimed but has no durable result")
		}
		var signed signedTransaction
		if err := json.Unmarshal(signedJSON, &signed); err != nil ||
			validateSignedResult(record, intentID, signed.TxType, signed) != nil {
			return nil, errors.New("stored Lighter signing result is invalid")
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, errors.New("commit Lighter signing retry")
		}
		return &signed, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("read Lighter signing request")
	}
	var unresolved bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM lighter_signing_requests
			WHERE credential_id = $1 AND status = 'claimed'
		)`, record.ID).Scan(&unresolved); err != nil {
		return nil, errors.New("check unresolved Lighter signing requests")
	}
	if unresolved {
		return nil, errors.New("Lighter signing request is claimed but has no durable result")
	}
	var expected int64
	if err := tx.QueryRow(ctx, `
		SELECT GREATEST($2::bigint, COALESCE(MAX(nonce) + 1, $2::bigint))
		FROM lighter_signing_requests
		WHERE credential_id = $1`, record.ID, changeNonce+1).Scan(&expected); err != nil {
		return nil, errors.New("read expected Lighter signing nonce")
	}
	if nonce != uint64(expected) {
		return nil, fmt.Errorf("Lighter signing nonce must equal %d", expected)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO lighter_signing_requests
			(credential_id, execution_account_id, nonce, intent_id, request_sha256, status)
		VALUES ($1, $2, $3, $4, $5, 'claimed')`, record.ID, record.ExecutionAccountID, nonce, intentID, requestSHA256); err != nil {
		return nil, errors.New("store Lighter signing nonce claim")
	}
	if err := appendAudit(ctx, tx, record, "transaction_nonce_claimed", map[string]any{
		"intentId": intentID,
		"nonce":    nonce,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, errors.New("commit Lighter signing nonce claim")
	}
	return nil, nil
}

func (value *pgStore) CompleteSigningRequest(ctx context.Context, record credential, intentID string, nonce uint64,
	requestSHA256 string, signed signedTransaction) error {
	if validateSignedResult(record, intentID, signed.TxType, signed) != nil {
		return errors.New("Lighter signing result identity mismatch")
	}
	encoded, err := json.Marshal(signed)
	if err != nil {
		return errors.New("encode Lighter signing result")
	}
	tx, err := value.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return errors.New("begin Lighter signing completion")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	var activeID *string
	if err := tx.QueryRow(ctx, `
		SELECT status, active_credential_id::text
		FROM lighter_credential_bindings
		WHERE execution_account_id = $1
		FOR SHARE`, record.ExecutionAccountID).Scan(&status, &activeID); err != nil || status != "linked" ||
		activeID == nil || *activeID != record.ID {
		return errors.New("execution account credential changed during signing completion")
	}
	command, err := tx.Exec(ctx, `
		UPDATE lighter_signing_requests
		SET status = 'signed', signed_transaction = $6::jsonb, signed_at = now()
		WHERE credential_id = $1 AND execution_account_id = $2 AND nonce = $3
		  AND intent_id = $4 AND request_sha256 = $5 AND status = 'claimed'`,
		record.ID, record.ExecutionAccountID, nonce, intentID, requestSHA256, encoded)
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("complete Lighter signing request")
	}
	if err := appendAudit(ctx, tx, record, "transaction_signed", map[string]any{
		"intentId": intentID, "nonce": nonce, "transactionHash": signed.TxHash, "transactionType": signed.TxType,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("commit Lighter signing completion")
	}
	return nil
}

func (value *pgStore) VerifyActive(ctx context.Context, record credential) error {
	var active bool
	err := value.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM lighter_credential_bindings AS binding
			JOIN lighter_credentials AS credential ON credential.id = binding.active_credential_id
			WHERE binding.execution_account_id = $1
			  AND binding.status = 'linked'
			  AND credential.id = $2
			  AND credential.status = 'linked'
		)`, record.ExecutionAccountID, record.ID).Scan(&active)
	if err != nil || !active {
		return errors.New("execution account credential changed during observation")
	}
	return nil
}

func (value *pgStore) ClaimAuthNonce(ctx context.Context, caller, nonce string, expiresAt time.Time) (bool, error) {
	_, _ = value.pool.Exec(ctx, `DELETE FROM lighter_provisioner_request_nonces WHERE expires_at <= now()`)
	command, err := value.pool.Exec(ctx, `
		INSERT INTO lighter_provisioner_request_nonces (caller, nonce, expires_at)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, caller, nonce, expiresAt)
	if err != nil {
		return false, errors.New("claim request nonce")
	}
	return command.RowsAffected() == 1, nil
}

func (value *pgStore) Audit(ctx context.Context, record credential, event string, details map[string]any) error {
	tx, err := value.pool.Begin(ctx)
	if err != nil {
		return errors.New("begin credential audit")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := appendAudit(ctx, tx, record, event, details); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("commit credential audit")
	}
	return nil
}

func (value *pgStore) AuditActive(ctx context.Context, record credential, event string, details map[string]any) error {
	tx, err := value.pool.Begin(ctx)
	if err != nil {
		return errors.New("begin active credential audit")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	var activeID *string
	if err := tx.QueryRow(ctx, `
		SELECT status, active_credential_id::text
		FROM lighter_credential_bindings
		WHERE execution_account_id = $1
		FOR SHARE`, record.ExecutionAccountID).Scan(&status, &activeID); err != nil {
		return errors.New("lock active credential binding")
	}
	if status != "linked" || activeID == nil || *activeID != record.ID {
		return errors.New("execution account credential changed during signing")
	}
	if err := appendAudit(ctx, tx, record, event, details); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("commit active credential audit")
	}
	return nil
}

const credentialSelect = `
	SELECT c.id::text, c.execution_account_id::text, c.owner_address,
		c.account_index, c.api_key_index, c.version, COALESCE(c.purpose, 'association'),
		COALESCE(c.replaces_credential_id::text, ''), COALESCE(c.public_key, ''),
		COALESCE(c.encrypted_data_key, ''::bytea), COALESCE(c.cipher_nonce, ''::bytea),
		COALESCE(c.ciphertext, ''::bytea), COALESCE(c.aad_sha256, ''::bytea),
		COALESCE(c.kms_key_id, ''), COALESCE(c.change_nonce, 0),
		COALESCE(c.expires_at_ms, 0), COALESCE(c.tx_type, 0),
		COALESCE(c.tx_hash, ''), COALESCE(c.tx_info, '{}'::jsonb)::text,
		COALESCE(c.message_to_sign, ''), COALESCE(c.l1_signature, ''),
		COALESCE(c.registered_public_key, ''),
		c.status, c.created_at, c.updated_at
	FROM lighter_credentials c `

type rowScanner interface {
	Scan(...any) error
}

func scanCredential(row rowScanner) (credential, error) {
	var value credential
	var txInfo string
	err := row.Scan(
		&value.ID, &value.ExecutionAccountID, &value.OwnerAddress,
		&value.AccountIndex, &value.APIKeyIndex, &value.Version, &value.Purpose,
		&value.ReplacesCredentialID, &value.PublicKey,
		&value.EncryptedDataKey, &value.CipherNonce, &value.Ciphertext,
		&value.AADDigest, &value.KMSKeyID, &value.ChangeNonce,
		&value.ExpiresAtMS, &value.TxType, &value.TxHash, &txInfo,
		&value.MessageToSign, &value.L1Signature, &value.RegisteredPublicKey, &value.Status,
		&value.CreatedAt, &value.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return credential{}, errNotFound
	}
	if err != nil {
		return credential{}, errors.New("read credential")
	}
	value.TxInfo = []byte(txInfo)
	return value, nil
}

func appendAudit(ctx context.Context, tx pgx.Tx, record credential, event string, details map[string]any) error {
	id, err := newUUID()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return errors.New("encode credential audit")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO lighter_credential_audit (
			id, execution_account_id, credential_id, event, details
		) VALUES ($1, $2, $3, $4, $5)`,
		id, record.ExecutionAccountID, record.ID, event, encoded); err != nil {
		return errors.New("append credential audit")
	}
	return nil
}

func newUUID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", errors.New("generate identifier")
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(bytes)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:]), nil
}
