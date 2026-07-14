package sequencerpublisher

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const stateSchema = `
CREATE TABLE IF NOT EXISTS sequencer_publisher_state (
    publisher_id text PRIMARY KEY,
    chain_id bigint NOT NULL CHECK (chain_id = 4663),
    feed_address text NOT NULL,
    signer_address text NOT NULL,
    last_latest_number bigint NOT NULL DEFAULT 0 CHECK (last_latest_number >= 0),
    last_latest_hash text NOT NULL DEFAULT '',
    last_finalized_number bigint NOT NULL DEFAULT 0 CHECK (last_finalized_number >= 0),
    last_finalized_hash text NOT NULL DEFAULT '',
    observed_healthy boolean NOT NULL DEFAULT false,
    continuous_started_at bigint NOT NULL DEFAULT 0 CHECK (continuous_started_at >= 0),
    observed_at timestamptz,
    last_sequence bigint NOT NULL DEFAULT 0 CHECK (last_sequence >= 0),
    last_nonce bigint CHECK (last_nonce >= 0),
    quarantined_reason text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (chain_id, signer_address)
);`

const transactionSchema = `
CREATE TABLE IF NOT EXISTS sequencer_publisher_transactions (
    publisher_id text NOT NULL REFERENCES sequencer_publisher_state(publisher_id),
    sequence bigint NOT NULL CHECK (sequence > 0),
    nonce bigint NOT NULL CHECK (nonce >= 0),
    tx_hash text NOT NULL UNIQUE,
    raw_transaction bytea NOT NULL,
    healthy boolean NOT NULL,
    started_at bigint NOT NULL CHECK (started_at > 0),
    status text NOT NULL CHECK (status IN ('signed', 'submitted', 'confirmed', 'reverted', 'quarantined')),
    error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    submitted_at timestamptz,
    confirmed_at timestamptz,
    PRIMARY KEY (publisher_id, sequence)
);
CREATE UNIQUE INDEX IF NOT EXISTS sequencer_publisher_one_pending
ON sequencer_publisher_transactions (publisher_id)
WHERE status IN ('signed', 'submitted');`

type PublisherState struct {
	Heads               HeadState
	ObservedHealthy     bool
	ContinuousStartedAt uint64
	ObservedAt          time.Time
	LastSequence        uint64
	LastNonce           *uint64
	QuarantinedReason   string
}

type PendingTransaction struct {
	Sequence  uint64
	Nonce     uint64
	Hash      common.Hash
	Raw       []byte
	Healthy   bool
	StartedAt uint64
	Status    string
	CreatedAt time.Time
}

type Journal struct {
	pool        *pgxpool.Pool
	publisherID string
	lock        *pgxpool.Conn
}

func OpenJournal(ctx context.Context, databaseURL, publisherID string, feed, signer common.Address) (*Journal, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("parse sequencer journal configuration")
	}
	config.MinConns = 1
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("connect sequencer journal")
	}
	if _, err := pool.Exec(ctx, stateSchema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("create sequencer publisher state: %w", err)
	}
	if _, err := pool.Exec(ctx, transactionSchema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("create sequencer publisher journal: %w", err)
	}
	address := strings.ToLower(feed.Hex())
	signerAddress := strings.ToLower(signer.Hex())
	command, err := pool.Exec(ctx, `
        INSERT INTO sequencer_publisher_state (publisher_id, chain_id, feed_address, signer_address)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (publisher_id) DO UPDATE SET updated_at = now()
        WHERE sequencer_publisher_state.chain_id = EXCLUDED.chain_id
          AND sequencer_publisher_state.feed_address = EXCLUDED.feed_address
          AND sequencer_publisher_state.signer_address = EXCLUDED.signer_address
    `, publisherID, chainID, address, signerAddress)
	if err != nil || command.RowsAffected() != 1 {
		pool.Close()
		return nil, errors.New("sequencer publisher binding conflicts with journal")
	}
	lock, err := pool.Acquire(ctx)
	if err != nil {
		pool.Close()
		return nil, errors.New("acquire sequencer publisher journal connection")
	}
	var acquired bool
	if err := lock.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, "sequencer-publisher:"+publisherID).Scan(&acquired); err != nil || !acquired {
		lock.Release()
		pool.Close()
		return nil, errors.New("another sequencer publisher instance owns this identity")
	}
	return &Journal{pool: pool, publisherID: publisherID, lock: lock}, nil
}

func (journal *Journal) Close(ctx context.Context) {
	if journal.lock != nil {
		_, _ = journal.lock.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, "sequencer-publisher:"+journal.publisherID)
		journal.lock.Release()
	}
	journal.pool.Close()
}

func (journal *Journal) State(ctx context.Context) (PublisherState, error) {
	return scanState(journal.pool.QueryRow(ctx, `
        SELECT last_latest_number, last_latest_hash, last_finalized_number, last_finalized_hash,
               observed_healthy, continuous_started_at, observed_at, last_sequence, last_nonce,
               quarantined_reason
        FROM sequencer_publisher_state WHERE publisher_id = $1
    `, journal.publisherID))
}

func (journal *Journal) RecordObservation(ctx context.Context, observation Observation, maxGap time.Duration) (PublisherState, error) {
	tx, err := journal.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return PublisherState{}, errors.New("begin sequencer observation")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	state, err := scanState(tx.QueryRow(ctx, `
        SELECT last_latest_number, last_latest_hash, last_finalized_number, last_finalized_hash,
               observed_healthy, continuous_started_at, observed_at, last_sequence, last_nonce,
               quarantined_reason
        FROM sequencer_publisher_state WHERE publisher_id = $1 FOR UPDATE
    `, journal.publisherID))
	if err != nil {
		return PublisherState{}, err
	}
	startedAt := nextStartedAt(state, observation.Healthy, observation.At, maxGap)
	heads := state.Heads
	if observation.Healthy {
		heads = observation.Heads
	}
	_, err = tx.Exec(ctx, `
        UPDATE sequencer_publisher_state
        SET last_latest_number = $2, last_latest_hash = $3,
            last_finalized_number = $4, last_finalized_hash = $5,
            observed_healthy = $6, continuous_started_at = $7, observed_at = $8,
            updated_at = now()
        WHERE publisher_id = $1
    `, journal.publisherID, heads.LatestNumber, hashText(heads.LatestHash),
		heads.FinalizedNumber, hashText(heads.FinalizedHash), observation.Healthy, startedAt, observation.At)
	if err != nil {
		return PublisherState{}, errors.New("record sequencer observation")
	}
	if err := tx.Commit(ctx); err != nil {
		return PublisherState{}, errors.New("commit sequencer observation")
	}
	state.Heads = heads
	state.ObservedHealthy = observation.Healthy
	state.ContinuousStartedAt = startedAt
	state.ObservedAt = observation.At
	return state, nil
}

func nextStartedAt(state PublisherState, healthy bool, now time.Time, maxGap time.Duration) uint64 {
	if healthy && state.ObservedHealthy && state.ContinuousStartedAt != 0 && !state.ObservedAt.IsZero() &&
		now.Sub(state.ObservedAt) >= 0 && now.Sub(state.ObservedAt) <= maxGap {
		return state.ContinuousStartedAt
	}
	return uint64(now.Unix())
}

func (journal *Journal) Pending(ctx context.Context) (*PendingTransaction, error) {
	var pending PendingTransaction
	var hash string
	err := journal.pool.QueryRow(ctx, `
        SELECT sequence, nonce, tx_hash, raw_transaction, healthy, started_at, status, created_at
        FROM sequencer_publisher_transactions
        WHERE publisher_id = $1 AND status IN ('signed', 'submitted')
        ORDER BY sequence DESC LIMIT 1
    `, journal.publisherID).Scan(&pending.Sequence, &pending.Nonce, &hash, &pending.Raw,
		&pending.Healthy, &pending.StartedAt, &pending.Status, &pending.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil || !validHash(hash) {
		return nil, errors.New("read pending sequencer transaction")
	}
	pending.Hash = common.HexToHash(hash)
	return &pending, nil
}

func (journal *Journal) RecordSigned(ctx context.Context, transaction PendingTransaction) error {
	tx, err := journal.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return errors.New("begin sequencer transaction journal")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var pending int
	if err := tx.QueryRow(ctx, `
        SELECT count(*) FROM sequencer_publisher_transactions
        WHERE publisher_id = $1 AND status IN ('signed', 'submitted')
    `, journal.publisherID).Scan(&pending); err != nil || pending != 0 {
		return errors.New("sequencer publisher already has a pending transaction")
	}
	command, err := tx.Exec(ctx, `
        INSERT INTO sequencer_publisher_transactions
            (publisher_id, sequence, nonce, tx_hash, raw_transaction, healthy, started_at, status)
        VALUES ($1, $2, $3, $4, $5, $6, $7, 'signed')
    `, journal.publisherID, transaction.Sequence, transaction.Nonce,
		strings.ToLower(transaction.Hash.Hex()), transaction.Raw, transaction.Healthy, transaction.StartedAt)
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("journal signed sequencer transaction")
	}
	command, err = tx.Exec(ctx, `
        UPDATE sequencer_publisher_state
        SET last_sequence = $2, last_nonce = $3, updated_at = now()
        WHERE publisher_id = $1 AND last_sequence < $2
          AND (last_nonce IS NULL OR last_nonce < $3)
    `, journal.publisherID, transaction.Sequence, transaction.Nonce)
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("advance sequencer transaction journal")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("commit signed sequencer transaction")
	}
	return nil
}

func (journal *Journal) MarkSubmitted(ctx context.Context, hash common.Hash) error {
	command, err := journal.pool.Exec(ctx, `
        UPDATE sequencer_publisher_transactions
        SET status = 'submitted', submitted_at = COALESCE(submitted_at, now())
        WHERE publisher_id = $1 AND tx_hash = $2 AND status IN ('signed', 'submitted')
    `, journal.publisherID, strings.ToLower(hash.Hex()))
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("mark sequencer transaction submitted")
	}
	return nil
}

func (journal *Journal) MarkReceipt(ctx context.Context, hash common.Hash, success bool) error {
	status := "confirmed"
	message := ""
	if !success {
		status = "reverted"
		message = "transaction reverted"
	}
	command, err := journal.pool.Exec(ctx, `
        UPDATE sequencer_publisher_transactions
        SET status = $3, error = $4, confirmed_at = now()
        WHERE publisher_id = $1 AND tx_hash = $2 AND status IN ('signed', 'submitted')
    `, journal.publisherID, strings.ToLower(hash.Hex()), status, message)
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("record sequencer transaction receipt")
	}
	return nil
}

func (journal *Journal) Quarantine(ctx context.Context, hash common.Hash, reason string) error {
	if len(reason) > 160 {
		reason = reason[:160]
	}
	tx, err := journal.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return errors.New("begin sequencer quarantine")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
        UPDATE sequencer_publisher_transactions SET status = 'quarantined', error = $3
        WHERE publisher_id = $1 AND tx_hash = $2 AND status IN ('signed', 'submitted')
    `, journal.publisherID, strings.ToLower(hash.Hex()), reason); err != nil {
		return errors.New("quarantine sequencer transaction")
	}
	if _, err := tx.Exec(ctx, `
        UPDATE sequencer_publisher_state SET quarantined_reason = $2, updated_at = now()
        WHERE publisher_id = $1
    `, journal.publisherID, reason); err != nil {
		return errors.New("quarantine sequencer publisher")
	}
	return tx.Commit(ctx)
}

type rowScanner interface {
	Scan(...any) error
}

func scanState(row rowScanner) (PublisherState, error) {
	var state PublisherState
	var latestHash, finalizedHash string
	var observedAt *time.Time
	var lastNonce *uint64
	err := row.Scan(&state.Heads.LatestNumber, &latestHash, &state.Heads.FinalizedNumber, &finalizedHash,
		&state.ObservedHealthy, &state.ContinuousStartedAt, &observedAt, &state.LastSequence,
		&lastNonce, &state.QuarantinedReason)
	if err != nil {
		return PublisherState{}, errors.New("read sequencer publisher state")
	}
	if latestHash != "" {
		state.Heads.LatestHash = common.HexToHash(latestHash)
	}
	if finalizedHash != "" {
		state.Heads.FinalizedHash = common.HexToHash(finalizedHash)
	}
	if observedAt != nil {
		state.ObservedAt = observedAt.UTC()
	}
	state.LastNonce = lastNonce
	return state, nil
}

func hashText(hash common.Hash) string {
	if hash == (common.Hash{}) {
		return ""
	}
	return strings.ToLower(hash.Hex())
}
