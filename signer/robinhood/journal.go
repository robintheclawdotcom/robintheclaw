package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Journal struct {
	pool         *pgxpool.Pool
	deploymentID string
	chainID      string
	signer       string
}

type TransactionRecord struct {
	RequestID     string
	IntentID      string
	PayloadSHA256 string
	Nonce         uint64
	TxHash        common.Hash
	Status        string
	BlockNumber   *uint64
	BlockHash     *common.Hash
	SignedTx      []byte
	Payload       []byte
}

type VerificationEvidence struct {
	PrimaryBlock   uint64
	PrimaryHash    common.Hash
	SecondaryBlock uint64
	SecondaryHash  common.Hash
}

type signedRecord struct {
	Submission
	PayloadSHA256  string
	Payload        []byte
	SignedTx       []byte
	MaxFee         *big.Int
	MaxPriorityFee *big.Int
	GasLimit       uint64
	Evidence       VerificationEvidence
}

type replacementRecord struct {
	RequestID       string
	IntentID        string
	Payload         []byte
	Nonce           uint64
	MaxFee          *big.Int
	MaxPriority     *big.Int
	GasLimit        uint64
	Status          string
	Depth           uint16
	FamilyCreatedAt time.Time
}

type nonceReservation interface {
	Nonce() uint64
	Commit(context.Context, signedRecord) error
	Rollback(context.Context)
}

type journalNonceReservation struct {
	journal *Journal
	tx      pgx.Tx
	nonce   uint64
	done    bool
}

func openJournal(
	ctx context.Context,
	databaseURL string,
	manifest deploymentManifest,
	deploymentID string,
) (*Journal, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("parse database configuration")
	}
	config.MinConns = 1
	config.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("connect signer journal")
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		pool.Close()
		return nil, errors.New("encode deployment manifest")
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO robinhood_signer_deployments
			(deployment_id, manifest, chain_id, signer_address, vault_address)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (deployment_id) DO NOTHING
	`, deploymentID, string(manifestJSON), manifest.ChainID, manifest.Signer, manifest.Vault)
	if err != nil {
		pool.Close()
		return nil, errors.New("register signer deployment")
	}
	return &Journal{
		pool:         pool,
		deploymentID: deploymentID,
		chainID:      manifest.ChainID,
		signer:       manifest.Signer,
	}, nil
}

func (journal *Journal) Close() {
	journal.pool.Close()
}

func (journal *Journal) Ready(ctx context.Context) bool {
	var table *string
	err := journal.pool.QueryRow(ctx, "SELECT to_regclass('public.robinhood_signer_transactions')::text").Scan(&table)
	if err != nil || table == nil {
		return false
	}
	var foreignPending int
	err = journal.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM robinhood_signer_transactions AS transactions
		JOIN robinhood_signer_deployments AS deployments USING (deployment_id)
		WHERE deployments.chain_id = $1 AND deployments.signer_address = $2
		  AND transactions.deployment_id <> $3
		  AND transactions.status IN (
		      'signed', 'submitted', 'soft_confirmed', 'l1_posted', 'ambiguous', 'replaced'
		  )
	`, journal.chainID, journal.signer, journal.deploymentID).Scan(&foreignPending)
	if err != nil || foreignPending != 0 {
		return false
	}
	var quarantined int
	err = journal.pool.QueryRow(ctx, `
		SELECT count(*) FROM robinhood_signer_transactions
		WHERE deployment_id = $1 AND status = 'quarantined'
	`, journal.deploymentID).Scan(&quarantined)
	return err == nil && quarantined == 0
}

func (journal *Journal) BeginNonce(ctx context.Context, chainID *big.Int, signer common.Address, observed uint64) (nonceReservation, error) {
	if observed > math.MaxInt64 {
		return nil, errors.New("observed nonce exceeds journal range")
	}
	transaction, err := journal.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, errors.New("start nonce transaction")
	}
	address := strings.ToLower(signer.Hex())
	_, err = transaction.Exec(ctx, `
		INSERT INTO robinhood_signer_nonces (chain_id, signer_address, next_nonce)
		VALUES ($1, $2, $3)
		ON CONFLICT (chain_id, signer_address) DO NOTHING
	`, chainID.String(), address, observed)
	if err != nil {
		_ = transaction.Rollback(ctx)
		return nil, errors.New("initialize nonce journal")
	}
	var stored uint64
	err = transaction.QueryRow(ctx, `
		SELECT next_nonce FROM robinhood_signer_nonces
		WHERE chain_id = $1 AND signer_address = $2
		FOR UPDATE
	`, chainID.String(), address).Scan(&stored)
	if err != nil {
		_ = transaction.Rollback(ctx)
		return nil, errors.New("lock nonce journal")
	}
	nonce := max(stored, observed)
	if nonce >= math.MaxInt64 {
		_ = transaction.Rollback(ctx)
		return nil, errors.New("nonce exhausted")
	}
	return &journalNonceReservation{journal: journal, tx: transaction, nonce: nonce}, nil
}

func (reservation *journalNonceReservation) Nonce() uint64 {
	return reservation.nonce
}

func (reservation *journalNonceReservation) Commit(ctx context.Context, record signedRecord) error {
	if reservation.done || record.Nonce != reservation.nonce {
		return errors.New("invalid nonce reservation")
	}
	if err := reservation.journal.insertTransaction(ctx, reservation.tx, record, nil, 0, time.Now().UTC()); err != nil {
		return err
	}
	command, err := reservation.tx.Exec(ctx, `
		UPDATE robinhood_signer_nonces
		SET next_nonce = $3, version = version + 1, updated_at = now()
		WHERE chain_id = $1 AND signer_address = $2 AND next_nonce <= $3
	`, reservation.journal.chainID, reservation.journal.signer, reservation.nonce+1)
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("advance nonce journal")
	}
	if err := reservation.tx.Commit(ctx); err != nil {
		return errors.New("commit signed transaction")
	}
	reservation.done = true
	return nil
}

func (reservation *journalNonceReservation) Rollback(ctx context.Context) {
	if !reservation.done {
		_ = reservation.tx.Rollback(ctx)
		reservation.done = true
	}
}

func (journal *Journal) Existing(ctx context.Context, requestID, digest string) (*Submission, error) {
	var submission Submission
	var storedDigest string
	err := journal.pool.QueryRow(ctx, `
		SELECT request_id, intent_id, tx_hash, nonce, status, payload_sha256
		FROM robinhood_signer_transactions
		WHERE deployment_id = $1 AND request_id = $2
	`, journal.deploymentID, requestID).Scan(
		&submission.RequestID,
		&submission.IntentID,
		&submission.TxHash,
		&submission.Nonce,
		&submission.Status,
		&storedDigest,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("read signer request")
	}
	if storedDigest != digest {
		return nil, errors.New("request_id was reused with a different payload")
	}
	return &submission, nil
}

func (journal *Journal) insertTransaction(
	ctx context.Context,
	transaction pgx.Tx,
	record signedRecord,
	replaces *string,
	depth uint16,
	familyCreatedAt time.Time,
) error {
	_, err := transaction.Exec(ctx, `
		INSERT INTO robinhood_signer_transactions
			(deployment_id, request_id, intent_id, payload_sha256, payload, nonce, tx_hash,
			 signed_transaction, max_fee_per_gas, max_priority_fee_per_gas,
			 gas_limit, status, replaces_request_id, replacement_depth, family_created_at,
			 primary_verified_block, primary_verified_hash,
			 secondary_verified_block, secondary_verified_hash)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'signed', $12, $13, $14,
			$15, $16, $17, $18
		)
	`,
		journal.deploymentID,
		record.RequestID,
		record.IntentID,
		record.PayloadSHA256,
		string(record.Payload),
		record.Nonce,
		strings.ToLower(record.TxHash),
		record.SignedTx,
		record.MaxFee.String(),
		record.MaxPriorityFee.String(),
		record.GasLimit,
		replaces,
		depth,
		familyCreatedAt,
		record.Evidence.PrimaryBlock,
		strings.ToLower(record.Evidence.PrimaryHash.Hex()),
		record.Evidence.SecondaryBlock,
		strings.ToLower(record.Evidence.SecondaryHash.Hex()),
	)
	if err != nil {
		return fmt.Errorf("persist signed transaction: %w", err)
	}
	return nil
}

func (journal *Journal) Replacement(ctx context.Context, requestID string) (*replacementRecord, error) {
	var record replacementRecord
	var payload string
	var maxFee string
	var maxPriority string
	err := journal.pool.QueryRow(ctx, `
		SELECT request_id, intent_id, payload::text, nonce, max_fee_per_gas::text,
		       max_priority_fee_per_gas::text, gas_limit, status,
		       replacement_depth, family_created_at
		FROM robinhood_signer_transactions
		WHERE deployment_id = $1 AND request_id = $2
	`, journal.deploymentID, requestID).Scan(
		&record.RequestID,
		&record.IntentID,
		&payload,
		&record.Nonce,
		&maxFee,
		&maxPriority,
		&record.GasLimit,
		&record.Status,
		&record.Depth,
		&record.FamilyCreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("replacement target does not exist")
	}
	if err != nil {
		return nil, errors.New("read replacement target")
	}
	if record.Status != "signed" && record.Status != "submitted" && record.Status != "ambiguous" {
		return nil, errors.New("transaction is not replaceable")
	}
	var ok bool
	record.MaxFee, ok = new(big.Int).SetString(maxFee, 10)
	if !ok {
		return nil, errors.New("invalid journaled fee")
	}
	record.MaxPriority, ok = new(big.Int).SetString(maxPriority, 10)
	if !ok {
		return nil, errors.New("invalid journaled priority fee")
	}
	record.Payload = []byte(payload)
	return &record, nil
}

func (journal *Journal) InsertReplacement(
	ctx context.Context,
	record signedRecord,
	replacement *replacementRecord,
) error {
	transaction, err := journal.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return errors.New("start replacement journal transaction")
	}
	defer transaction.Rollback(ctx)
	if replacement == nil || replacement.Depth == math.MaxUint16 {
		return errors.New("invalid replacement record")
	}
	if err := journal.insertTransaction(
		ctx,
		transaction,
		record,
		&replacement.RequestID,
		replacement.Depth+1,
		replacement.FamilyCreatedAt,
	); err != nil {
		return err
	}
	command, err := transaction.Exec(ctx, `
		UPDATE robinhood_signer_transactions
		SET status = 'replaced', replaced_by_request_id = $2, updated_at = now()
		WHERE deployment_id = $3 AND request_id = $1
		  AND replaced_by_request_id IS NULL
		  AND status IN ('signed', 'submitted', 'ambiguous')
	`, replacement.RequestID, record.RequestID, journal.deploymentID)
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("replacement target changed")
	}
	if err := transaction.Commit(ctx); err != nil {
		return errors.New("commit replacement journal transaction")
	}
	return nil
}

func (journal *Journal) SetSubmitted(ctx context.Context, requestID string) error {
	command, err := journal.pool.Exec(ctx, `
		UPDATE robinhood_signer_transactions
		SET status = 'submitted', error = NULL, reconcile_attempts = 0,
		    next_reconcile_at = now(), updated_at = now()
		WHERE deployment_id = $1 AND request_id = $2
		  AND status IN ('signed', 'submitted', 'ambiguous')
	`, journal.deploymentID, requestID)
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("record submitted transaction")
	}
	return nil
}

func (journal *Journal) SetAmbiguous(ctx context.Context, requestID, reason string) error {
	command, err := journal.pool.Exec(ctx, `
		UPDATE robinhood_signer_transactions
		SET status = 'ambiguous', block_number = NULL, block_hash = NULL,
		    error = $3, next_reconcile_at = now(), updated_at = now()
		WHERE deployment_id = $1 AND request_id = $2
		  AND status IN ('signed', 'soft_confirmed', 'l1_posted')
	`, journal.deploymentID, requestID, reason)
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("record ambiguous transaction")
	}
	return nil
}

func (journal *Journal) Pending(ctx context.Context, limit int) ([]TransactionRecord, error) {
	rows, err := journal.pool.Query(ctx, `
		SELECT request_id, intent_id, payload_sha256, tx_hash, nonce, status,
		       block_number, block_hash, signed_transaction, payload::text
		FROM robinhood_signer_transactions
		WHERE deployment_id = $1 AND next_reconcile_at <= now()
		  AND status IN ('signed', 'submitted', 'soft_confirmed', 'l1_posted', 'ambiguous', 'replaced')
		ORDER BY next_reconcile_at, updated_at
		LIMIT $2
	`, journal.deploymentID, limit)
	if err != nil {
		return nil, errors.New("read pending transactions")
	}
	defer rows.Close()
	var records []TransactionRecord
	for rows.Next() {
		var record TransactionRecord
		var txHash string
		var blockNumber *uint64
		var blockHash *string
		var payload string
		if err := rows.Scan(
			&record.RequestID,
			&record.IntentID,
			&record.PayloadSHA256,
			&txHash,
			&record.Nonce,
			&record.Status,
			&blockNumber,
			&blockHash,
			&record.SignedTx,
			&payload,
		); err != nil {
			return nil, errors.New("decode pending transaction")
		}
		record.TxHash = common.HexToHash(txHash)
		record.BlockNumber = blockNumber
		record.Payload = []byte(payload)
		if blockHash != nil {
			value := common.HexToHash(*blockHash)
			record.BlockHash = &value
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (journal *Journal) SetReceipt(ctx context.Context, requestID, status string, blockNumber uint64, blockHash common.Hash, reason string) error {
	command, err := journal.pool.Exec(ctx, `
		UPDATE robinhood_signer_transactions
		SET status = $3, reconcile_attempts = 0,
		    block_number = $4,
		    block_hash = $5,
		    error = NULLIF($6, ''),
		    updated_at = now()
		WHERE deployment_id = $1 AND request_id = $2
	`, journal.deploymentID, requestID, status, blockNumber, strings.ToLower(blockHash.Hex()), reason)
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("record transaction receipt")
	}
	return nil
}

func (journal *Journal) SetSuperseded(ctx context.Context, requestID, intentID string, nonce uint64) error {
	_, err := journal.pool.Exec(ctx, `
		UPDATE robinhood_signer_transactions
		SET status = 'superseded', updated_at = now()
		WHERE deployment_id = $1 AND intent_id = $3 AND nonce = $4 AND request_id <> $2
		  AND status IN ('signed', 'submitted', 'soft_confirmed', 'l1_posted', 'ambiguous', 'replaced')
	`, journal.deploymentID, requestID, intentID, nonce)
	return err
}

func (journal *Journal) SetFinality(ctx context.Context, requestID, status string) error {
	if status != "l1_posted" && status != "ethereum_final" {
		return errors.New("invalid finality status")
	}
	command, err := journal.pool.Exec(ctx, `
		UPDATE robinhood_signer_transactions
		SET status = $3, updated_at = now()
		WHERE deployment_id = $1 AND request_id = $2
		  AND status IN ('soft_confirmed', 'l1_posted')
	`, journal.deploymentID, requestID, status)
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("record transaction finality")
	}
	return nil
}

func (journal *Journal) DeferReconcile(ctx context.Context, requestID string) error {
	command, err := journal.pool.Exec(ctx, `
		UPDATE robinhood_signer_transactions
		SET reconcile_attempts = reconcile_attempts + 1,
		    last_checked_at = now(),
		    next_reconcile_at = now()
		        + make_interval(secs => LEAST(60, power(2, LEAST(reconcile_attempts, 5))::integer)),
		    updated_at = now()
		WHERE deployment_id = $1 AND request_id = $2
	`, journal.deploymentID, requestID)
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("schedule transaction reconciliation")
	}
	return nil
}

func (journal *Journal) Quarantine(ctx context.Context, requestID, reason string) error {
	_, err := journal.pool.Exec(ctx, `
		UPDATE robinhood_signer_transactions
		SET status = 'quarantined', error = $3, updated_at = now()
		WHERE deployment_id = $1 AND request_id = $2
	`, journal.deploymentID, requestID, reason)
	return err
}

func (journal *Journal) ClaimAuthNonce(ctx context.Context, nonce string, expiresAt time.Time) error {
	transaction, err := journal.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return errors.New("start authorization nonce transaction")
	}
	defer transaction.Rollback(ctx)
	if _, err := transaction.Exec(ctx, `
		DELETE FROM robinhood_signer_auth_nonces WHERE expires_at < now()
	`); err != nil {
		return errors.New("expire authorization nonces")
	}
	_, err = transaction.Exec(ctx, `
		INSERT INTO robinhood_signer_auth_nonces (deployment_id, nonce, expires_at)
		VALUES ($1, $2, $3)
	`, journal.deploymentID, nonce, expiresAt)
	if err != nil {
		return errors.New("authorization nonce was already used")
	}
	if err := transaction.Commit(ctx); err != nil {
		return errors.New("commit authorization nonce")
	}
	return nil
}
