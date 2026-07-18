package aaplrelay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const stateSchema = `
CREATE TABLE IF NOT EXISTS aapl_relay_state (
    publisher_id text PRIMARY KEY,
    source_chain_id bigint NOT NULL,
    source_feed text NOT NULL,
    source_code_hash text NOT NULL,
    target_chain_id bigint NOT NULL,
    target_feed text NOT NULL,
    target_code_hash text NOT NULL,
    signer_address text NOT NULL,
    last_source_round numeric(24,0) NOT NULL DEFAULT 0,
    last_source_answer numeric(58,0) NOT NULL DEFAULT 0,
    last_source_updated_at bigint NOT NULL DEFAULT 0,
    last_answered_in_round numeric(24,0) NOT NULL DEFAULT 0,
    last_source_block bigint NOT NULL DEFAULT 0,
    last_source_block_hash text NOT NULL DEFAULT '',
    last_sequence bigint NOT NULL DEFAULT 0,
    last_nonce bigint,
    quarantined_reason text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
)`

const transactionSchema = `
CREATE TABLE IF NOT EXISTS aapl_relay_transactions (
    publisher_id text NOT NULL REFERENCES aapl_relay_state(publisher_id),
    sequence bigint NOT NULL,
    nonce bigint NOT NULL,
    tx_hash text NOT NULL,
    raw_transaction bytea NOT NULL,
    source_round numeric(24,0) NOT NULL,
    source_answer numeric(58,0) NOT NULL,
    source_updated_at bigint NOT NULL,
    answered_in_round numeric(24,0) NOT NULL,
    status text NOT NULL CHECK (status IN ('signed', 'submitted', 'confirmed', 'failed', 'quarantined')),
    error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (publisher_id, sequence),
    UNIQUE (publisher_id, nonce),
    UNIQUE (tx_hash)
);
CREATE UNIQUE INDEX IF NOT EXISTS aapl_relay_one_pending
ON aapl_relay_transactions (publisher_id)
WHERE status IN ('signed', 'submitted')`

type RelayState struct {
	Observation       PriceObservation
	LastSequence      uint64
	LastNonce         *uint64
	QuarantinedReason string
}

type PendingTransaction struct {
	Sequence    uint64
	Nonce       uint64
	Hash        common.Hash
	Raw         []byte
	Observation PriceObservation
	Status      string
	CreatedAt   time.Time
}

type Journal struct {
	pool        *pgxpool.Pool
	publisherID string
	lock        *pgxpool.Conn
}

func OpenJournal(ctx context.Context, config Config) (*Journal, error) {
	poolConfig, err := pgxpool.ParseConfig(config.DatabaseURL)
	if err != nil {
		return nil, errors.New("parse AAPL relay journal configuration")
	}
	poolConfig.MinConns = 1
	poolConfig.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, errors.New("connect AAPL relay journal")
	}
	if config.RunMigrations {
		for _, schema := range []string{stateSchema, transactionSchema} {
			if _, err := pool.Exec(ctx, schema); err != nil {
				pool.Close()
				return nil, fmt.Errorf("create AAPL relay journal: %w", err)
			}
		}
	}
	command, err := pool.Exec(ctx, `
        INSERT INTO aapl_relay_state (
            publisher_id, source_chain_id, source_feed, source_code_hash,
            target_chain_id, target_feed, target_code_hash, signer_address
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        ON CONFLICT (publisher_id) DO UPDATE SET updated_at = now()
        WHERE aapl_relay_state.source_chain_id = EXCLUDED.source_chain_id
          AND aapl_relay_state.source_feed = EXCLUDED.source_feed
          AND aapl_relay_state.source_code_hash = EXCLUDED.source_code_hash
          AND aapl_relay_state.target_chain_id = EXCLUDED.target_chain_id
          AND aapl_relay_state.target_feed = EXCLUDED.target_feed
          AND aapl_relay_state.target_code_hash = EXCLUDED.target_code_hash
          AND aapl_relay_state.signer_address = EXCLUDED.signer_address
    `, config.PublisherID, SourceChainID, lowerAddress(config.SourceFeed), lowerHash(config.SourceCodeHash),
		TargetChainID, lowerAddress(config.TargetFeed), lowerHash(config.TargetCodeHash),
		lowerAddress(config.SignerAddress))
	if err != nil || command.RowsAffected() != 1 {
		pool.Close()
		return nil, errors.New("AAPL relay binding conflicts with journal")
	}
	lock, err := pool.Acquire(ctx)
	if err != nil {
		pool.Close()
		return nil, errors.New("acquire AAPL relay journal connection")
	}
	var acquired bool
	lockName := "aapl-relay:" + config.PublisherID
	if err := lock.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, lockName).Scan(&acquired); err != nil || !acquired {
		lock.Release()
		pool.Close()
		return nil, errors.New("another AAPL relay instance owns this identity")
	}
	return &Journal{pool: pool, publisherID: config.PublisherID, lock: lock}, nil
}

func (journal *Journal) Close(ctx context.Context) {
	if journal.lock != nil {
		_, _ = journal.lock.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, "aapl-relay:"+journal.publisherID)
		journal.lock.Release()
	}
	journal.pool.Close()
}

func (journal *Journal) State(ctx context.Context) (RelayState, error) {
	return scanState(journal.pool.QueryRow(ctx, stateQuery, journal.publisherID))
}

const stateQuery = `
SELECT last_source_round::text, last_source_answer::text, last_source_updated_at,
       last_answered_in_round::text, last_source_block, last_source_block_hash,
       last_sequence, last_nonce, quarantined_reason
FROM aapl_relay_state WHERE publisher_id = $1`

func (journal *Journal) RecordObservation(ctx context.Context, observation PriceObservation) (RelayState, error) {
	tx, err := journal.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RelayState{}, errors.New("begin AAPL source observation")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	state, err := scanState(tx.QueryRow(ctx, stateQuery+` FOR UPDATE`, journal.publisherID))
	if err != nil {
		return RelayState{}, err
	}
	if reason := sourceConflict(state.Observation, observation); reason != "" {
		if _, err := tx.Exec(ctx, `
            UPDATE aapl_relay_state SET quarantined_reason = $2, updated_at = now()
            WHERE publisher_id = $1
        `, journal.publisherID, reason); err != nil {
			return RelayState{}, errors.New("quarantine AAPL source observation")
		}
		if err := tx.Commit(ctx); err != nil {
			return RelayState{}, errors.New("commit AAPL source quarantine")
		}
		return RelayState{}, errors.New(reason)
	}
	_, err = tx.Exec(ctx, `
        UPDATE aapl_relay_state
        SET last_source_round = $2, last_source_answer = $3, last_source_updated_at = $4,
            last_answered_in_round = $5, last_source_block = $6,
            last_source_block_hash = $7, updated_at = now()
        WHERE publisher_id = $1
    `, journal.publisherID, observation.RoundID.String(), observation.Answer.String(),
		observation.UpdatedAt, observation.AnsweredInRound.String(), observation.BlockNumber,
		lowerHash(observation.BlockHash))
	if err != nil {
		return RelayState{}, errors.New("record AAPL source observation")
	}
	if err := tx.Commit(ctx); err != nil {
		return RelayState{}, errors.New("commit AAPL source observation")
	}
	state.Observation = observation
	return state, nil
}

func sourceConflict(previous, next PriceObservation) string {
	if previous.RoundID == nil || previous.RoundID.Sign() == 0 {
		return ""
	}
	if next.RoundID.Cmp(previous.RoundID) < 0 {
		return "AAPL source round regressed"
	}
	if next.BlockNumber < previous.BlockNumber ||
		(next.BlockNumber == previous.BlockNumber && next.BlockHash != previous.BlockHash) {
		return "AAPL source finalized block regressed"
	}
	if next.RoundID.Cmp(previous.RoundID) == 0 &&
		(next.Answer.Cmp(previous.Answer) != 0 || next.UpdatedAt != previous.UpdatedAt ||
			next.AnsweredInRound.Cmp(previous.AnsweredInRound) != 0) {
		return "AAPL source round mutated"
	}
	if next.UpdatedAt < previous.UpdatedAt {
		return "AAPL source timestamp regressed"
	}
	return ""
}

func (journal *Journal) Pending(ctx context.Context) (*PendingTransaction, error) {
	var pending PendingTransaction
	var hash, round, answer, answered string
	err := journal.pool.QueryRow(ctx, `
        SELECT sequence, nonce, tx_hash, raw_transaction, source_round::text,
               source_answer::text, source_updated_at, answered_in_round::text,
               status, created_at
        FROM aapl_relay_transactions
        WHERE publisher_id = $1 AND status IN ('signed', 'submitted')
        ORDER BY sequence DESC LIMIT 1
    `, journal.publisherID).Scan(
		&pending.Sequence, &pending.Nonce, &hash, &pending.Raw, &round, &answer,
		&pending.Observation.UpdatedAt, &answered, &pending.Status, &pending.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil || !validHash(hash) {
		return nil, errors.New("read pending AAPL relay transaction")
	}
	pending.Hash = common.HexToHash(hash)
	if pending.Observation.RoundID, err = parseNumber(round); err != nil {
		return nil, errors.New("decode pending AAPL source round")
	}
	if pending.Observation.Answer, err = parseNumber(answer); err != nil {
		return nil, errors.New("decode pending AAPL source answer")
	}
	if pending.Observation.AnsweredInRound, err = parseNumber(answered); err != nil {
		return nil, errors.New("decode pending AAPL answered round")
	}
	return &pending, nil
}

func (journal *Journal) RecordSigned(ctx context.Context, pending PendingTransaction) error {
	tx, err := journal.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return errors.New("begin AAPL relay transaction journal")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var pendingCount int
	if err := tx.QueryRow(ctx, `
        SELECT count(*) FROM aapl_relay_transactions
        WHERE publisher_id = $1 AND status IN ('signed', 'submitted')
    `, journal.publisherID).Scan(&pendingCount); err != nil || pendingCount != 0 {
		return errors.New("AAPL relay already has a pending transaction")
	}
	_, err = tx.Exec(ctx, `
        INSERT INTO aapl_relay_transactions (
            publisher_id, sequence, nonce, tx_hash, raw_transaction, source_round,
            source_answer, source_updated_at, answered_in_round, status, created_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'signed',$10)
    `, journal.publisherID, pending.Sequence, pending.Nonce, lowerHash(pending.Hash), pending.Raw,
		pending.Observation.RoundID.String(), pending.Observation.Answer.String(),
		pending.Observation.UpdatedAt, pending.Observation.AnsweredInRound.String(), pending.CreatedAt)
	if err != nil {
		return errors.New("journal signed AAPL relay transaction")
	}
	_, err = tx.Exec(ctx, `
        UPDATE aapl_relay_state SET last_sequence = $2, last_nonce = $3, updated_at = now()
        WHERE publisher_id = $1
    `, journal.publisherID, pending.Sequence, pending.Nonce)
	if err != nil {
		return errors.New("advance AAPL relay journal")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("commit signed AAPL relay transaction")
	}
	return nil
}

func (journal *Journal) MarkSubmitted(ctx context.Context, hash common.Hash) error {
	command, err := journal.pool.Exec(ctx, `
        UPDATE aapl_relay_transactions SET status = 'submitted', updated_at = now()
        WHERE publisher_id = $1 AND tx_hash = $2 AND status = 'signed'
    `, journal.publisherID, lowerHash(hash))
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("mark AAPL relay transaction submitted")
	}
	return nil
}

func (journal *Journal) MarkReceipt(ctx context.Context, hash common.Hash, success bool) error {
	status := "failed"
	if success {
		status = "confirmed"
	}
	command, err := journal.pool.Exec(ctx, `
        UPDATE aapl_relay_transactions SET status = $3, updated_at = now()
        WHERE publisher_id = $1 AND tx_hash = $2 AND status IN ('signed', 'submitted')
    `, journal.publisherID, lowerHash(hash), status)
	if err != nil || command.RowsAffected() != 1 {
		return errors.New("record AAPL relay transaction receipt")
	}
	return nil
}

func (journal *Journal) Quarantine(ctx context.Context, hash common.Hash, reason string) error {
	tx, err := journal.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return errors.New("begin AAPL relay quarantine")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if hash != (common.Hash{}) {
		_, err = tx.Exec(ctx, `
            UPDATE aapl_relay_transactions
            SET status = 'quarantined', error = $3, updated_at = now()
            WHERE publisher_id = $1 AND tx_hash = $2 AND status IN ('signed', 'submitted')
        `, journal.publisherID, lowerHash(hash), reason)
		if err != nil {
			return errors.New("quarantine AAPL relay transaction")
		}
	}
	_, err = tx.Exec(ctx, `
        UPDATE aapl_relay_state SET quarantined_reason = $2, updated_at = now()
        WHERE publisher_id = $1
    `, journal.publisherID, reason)
	if err != nil {
		return errors.New("quarantine AAPL relay publisher")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("commit AAPL relay quarantine")
	}
	return nil
}

func scanState(row pgx.Row) (RelayState, error) {
	var state RelayState
	var round, answer, answered, blockHash string
	var nonce sql.NullInt64
	err := row.Scan(
		&round, &answer, &state.Observation.UpdatedAt, &answered,
		&state.Observation.BlockNumber, &blockHash, &state.LastSequence, &nonce,
		&state.QuarantinedReason,
	)
	if err != nil {
		return RelayState{}, errors.New("read AAPL relay state")
	}
	if state.Observation.RoundID, err = parseNumber(round); err != nil {
		return RelayState{}, errors.New("decode AAPL relay source round")
	}
	if state.Observation.Answer, err = parseNumber(answer); err != nil {
		return RelayState{}, errors.New("decode AAPL relay source answer")
	}
	if state.Observation.AnsweredInRound, err = parseNumber(answered); err != nil {
		return RelayState{}, errors.New("decode AAPL relay answered round")
	}
	if blockHash != "" {
		if !validHash(blockHash) {
			return RelayState{}, errors.New("decode AAPL relay source block hash")
		}
		state.Observation.BlockHash = common.HexToHash(blockHash)
	}
	if nonce.Valid {
		if nonce.Int64 < 0 {
			return RelayState{}, errors.New("decode AAPL relay nonce")
		}
		value := uint64(nonce.Int64)
		state.LastNonce = &value
	}
	return state, nil
}

func parseNumber(value string) (*big.Int, error) {
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok || parsed.Sign() < 0 {
		return nil, errors.New("invalid numeric value")
	}
	return parsed, nil
}

func validHash(value string) bool {
	return len(value) == 66 && strings.HasPrefix(value, "0x")
}

func lowerAddress(value common.Address) string { return strings.ToLower(value.Hex()) }
func lowerHash(value common.Hash) string       { return strings.ToLower(value.Hex()) }
