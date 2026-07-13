package scheduler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGStore struct {
	pool     *pgxpool.Pool
	workerID string
}

func NewPGStore(pool *pgxpool.Pool, workerID string) (*PGStore, error) {
	if pool == nil || !accountPattern.MatchString(workerID) {
		return nil, errors.New("pool and valid worker ID are required")
	}
	return &PGStore{pool: pool, workerID: workerID}, nil
}

func (s *PGStore) Claim(ctx context.Context, now time.Time, lease time.Duration) (*Dispatch, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row := tx.QueryRow(ctx, `
SELECT a.evaluation_id, a.execution_account_id, a.agent_id, a.approval_sha256,
       a.expires_at, a.evaluation, a.readiness, a.account_state,
       w.request_id, w.requested_at_ms, w.quote_body, w.quote_sha256,
       w.runner_body, w.runner_sha256
FROM live_scheduler_work w
JOIN live_scheduler_approvals a USING (evaluation_id, execution_account_id)
JOIN execution_accounts account
  ON account.execution_account_id = a.execution_account_id
 AND account.agent_id = a.agent_id
 AND account.strategy_version = $1
 AND account.strategy_manifest_sha256 = $2
 AND account.status = 'active'
JOIN execution_account_registrations registration
  ON registration.execution_account_id = account.execution_account_id
 AND registration.agent_id = account.agent_id
 AND registration.strategy_version = account.strategy_version
 AND registration.strategy_manifest_sha256 = account.strategy_manifest_sha256
 AND registration.risk_version = account.risk_version
 AND registration.lighter_account_index = account.lighter_account_index
 AND registration.lighter_api_key_index = account.lighter_api_key_index
 AND registration.robinhood_owner = account.owner_address
 AND registration.robinhood_vault = account.robinhood_vault
 AND registration.robinhood_signer = account.robinhood_signer
 AND registration.binding_sha256 = account.binding_sha256
WHERE w.state IN ('pending', 'quoted', 'running', 'ambiguous')
  AND (w.lease_until IS NULL OR w.lease_until <= $3)
ORDER BY a.approved_at, a.execution_account_id
FOR UPDATE OF w SKIP LOCKED
LIMIT 1`, StrategyVersion, StrategyManifestSHA256, now)
	var dispatch Dispatch
	var evaluation, readiness, accountState []byte
	var requestID, quoteSHA, runnerSHA *string
	var requestedAt *int64
	if err := row.Scan(&dispatch.EvaluationID, &dispatch.ExecutionAccountID, &dispatch.AgentID,
		&dispatch.ApprovalSHA256, &dispatch.ExpiresAt, &evaluation, &readiness, &accountState,
		&requestID, &requestedAt, &dispatch.QuoteBody, &quoteSHA, &dispatch.RunnerBody, &runnerSHA); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, tx.Commit(ctx)
		}
		return nil, err
	}
	if err := decodeStrict(evaluation, &dispatch.Evaluation); err != nil {
		return nil, fmt.Errorf("decode stored evaluation: %w", err)
	}
	if err := decodeStrict(readiness, &dispatch.Readiness); err != nil {
		return nil, fmt.Errorf("decode stored readiness: %w", err)
	}
	if err := decodeStrict(accountState, &dispatch.AccountState); err != nil {
		return nil, fmt.Errorf("decode stored account state: %w", err)
	}
	if requestID != nil {
		dispatch.RequestID = *requestID
	}
	if requestedAt != nil {
		dispatch.RequestedAtMS = uint64(*requestedAt)
	}
	if quoteSHA != nil {
		dispatch.QuoteSHA256 = *quoteSHA
	}
	if runnerSHA != nil {
		dispatch.RunnerSHA256 = *runnerSHA
	}
	command, err := tx.Exec(ctx, `
UPDATE live_scheduler_work
SET state = 'running', lease_owner = $3, lease_until = $4, attempt = attempt + 1, updated_at = $5
WHERE evaluation_id = $1 AND execution_account_id = $2`, dispatch.EvaluationID, dispatch.ExecutionAccountID,
		s.workerID, now.Add(lease), now)
	if err != nil {
		return nil, fmt.Errorf("lease scheduler dispatch: %w", err)
	}
	if command.RowsAffected() != 1 {
		return nil, errors.New("lease scheduler dispatch affected no row")
	}
	if err := insertEvent(ctx, tx, dispatch, "claimed", map[string]any{"worker_id": s.workerID}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &dispatch, nil
}

func (s *PGStore) Eligible(ctx context.Context, dispatch Dispatch) (bool, error) {
	var eligible bool
	err := s.pool.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM execution_accounts account
  JOIN execution_account_registrations registration
    ON registration.execution_account_id = account.execution_account_id
   AND registration.agent_id = account.agent_id
   AND registration.strategy_version = account.strategy_version
   AND registration.strategy_manifest_sha256 = account.strategy_manifest_sha256
   AND registration.risk_version = account.risk_version
   AND registration.binding_sha256 = account.binding_sha256
  JOIN execution_account_control account_control USING (execution_account_id)
  JOIN execution_account_readiness coordinator_readiness USING (execution_account_id)
  JOIN execution_strategy_control strategy_control
    ON strategy_control.strategy_version = account.strategy_version
   AND strategy_control.strategy_manifest_sha256 = account.strategy_manifest_sha256
  CROSS JOIN execution_control global_control
  CROSS JOIN LATERAL (
    SELECT to_state
    FROM execution_promotion_events
    WHERE strategy_version = account.strategy_version
    ORDER BY id DESC
    LIMIT 1
  ) promotion
  WHERE account.execution_account_id = $1
    AND account.agent_id = $2
    AND account.strategy_version = $3
    AND account.strategy_manifest_sha256 = $4
    AND account.status = 'active'
    AND account_control.mode = 'ACTIVE'
    AND strategy_control.mode = 'ACTIVE'
    AND global_control.mode = 'ACTIVE'
    AND promotion.to_state = 'canary_eligible'
    AND coordinator_readiness.venue_approved
    AND coordinator_readiness.oracle_healthy
    AND coordinator_readiness.sequencer_healthy
    AND coordinator_readiness.reconciliation_ready
    AND coordinator_readiness.exit_authority_ready
    AND coordinator_readiness.alerting_ready
    AND coordinator_readiness.safe_rotation_ready
)`, dispatch.ExecutionAccountID, dispatch.AgentID, StrategyVersion, StrategyManifestSHA256).Scan(&eligible)
	return eligible, err
}

func (s *PGStore) PrepareQuote(ctx context.Context, dispatch Dispatch, requestID string, requestedAt uint64) error {
	return s.update(ctx, dispatch, "quote_prepared", map[string]any{"request_id": requestID}, `
UPDATE live_scheduler_work
SET request_id = $3, requested_at_ms = $4, updated_at = now()
WHERE evaluation_id = $1 AND execution_account_id = $2 AND lease_owner = $5
  AND request_id IS NULL`, requestID, int64(requestedAt), s.workerID)
}

func (s *PGStore) SaveQuote(ctx context.Context, dispatch Dispatch, body []byte, sha string) error {
	return s.update(ctx, dispatch, "quote_persisted", map[string]any{"quote_sha256": sha}, `
UPDATE live_scheduler_work
SET state = 'quoted', quote_body = $3, quote_sha256 = $4, updated_at = now()
WHERE evaluation_id = $1 AND execution_account_id = $2 AND lease_owner = $5
  AND quote_body IS NULL`, body, sha, s.workerID)
}

func (s *PGStore) SaveRunner(ctx context.Context, dispatch Dispatch, body []byte, sha string) error {
	return s.update(ctx, dispatch, "runner_prepared", map[string]any{"runner_sha256": sha}, `
UPDATE live_scheduler_work
SET state = 'running', runner_body = $3, runner_sha256 = $4, updated_at = now()
WHERE evaluation_id = $1 AND execution_account_id = $2 AND lease_owner = $5
  AND runner_body IS NULL`, body, sha, s.workerID)
}

func (s *PGStore) Complete(ctx context.Context, dispatch Dispatch, body []byte, sha string) error {
	return s.finish(ctx, dispatch, "succeeded", "completed", body, sha, "")
}

func (s *PGStore) Ambiguous(ctx context.Context, dispatch Dispatch, body []byte, sha string) error {
	return s.finish(ctx, dispatch, "ambiguous", "runner_ambiguous", body, sha, "")
}

func (s *PGStore) Retry(ctx context.Context, dispatch Dispatch, reason string) error {
	return s.finish(ctx, dispatch, "pending", "retry_scheduled", nil, "", reason)
}

func (s *PGStore) Block(ctx context.Context, dispatch Dispatch, reason string) error {
	return s.finish(ctx, dispatch, "blocked", "blocked", nil, "", reason)
}

func (s *PGStore) update(ctx context.Context, dispatch Dispatch, event string, details map[string]any, query string, args ...any) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	params := append([]any{dispatch.EvaluationID, dispatch.ExecutionAccountID}, args...)
	command, err := tx.Exec(ctx, query, params...)
	if err != nil {
		return fmt.Errorf("update scheduler dispatch: %w", err)
	}
	if command.RowsAffected() != 1 {
		return errors.New("scheduler dispatch lease changed concurrently")
	}
	if err := insertEvent(ctx, tx, dispatch, event, details); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PGStore) finish(ctx context.Context, dispatch Dispatch, state, event string, body []byte, sha, reason string) error {
	if len(reason) > 240 {
		reason = reason[:240]
	}
	details := map[string]any{"state": state}
	if len(body) != 0 {
		details["outcome_body_base64"] = base64.StdEncoding.EncodeToString(body)
		details["outcome_sha256"] = sha
	}
	if reason != "" {
		details["reason"] = reason
	}
	return s.update(ctx, dispatch, event, details, `
UPDATE live_scheduler_work
SET state = $3, outcome_body = NULLIF($4, ''::bytea), outcome_sha256 = NULLIF($5, ''),
    last_error = NULLIF($6, ''), lease_owner = NULL, lease_until = NULL, updated_at = now()
WHERE evaluation_id = $1 AND execution_account_id = $2 AND lease_owner = $7`, state, body, sha, reason, s.workerID)
}

func insertEvent(ctx context.Context, tx pgx.Tx, dispatch Dispatch, kind string, details map[string]any) error {
	body, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO live_scheduler_events
  (evaluation_id, execution_account_id, kind, details, details_sha256)
VALUES ($1, $2, $3, $4, $5)`, dispatch.EvaluationID, dispatch.ExecutionAccountID, kind, body, digest(body))
	return err
}
