package evaluation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ResearchSource interface {
	Candidates(context.Context, time.Time) ([]PaperCandidate, error)
	Exits(context.Context, time.Time) ([]PaperExit, error)
}

func (source *PGResearchSource) Exits(ctx context.Context, now time.Time) ([]PaperExit, error) {
	rows, err := source.pool.Query(ctx, `
SELECT evaluation.id::text, evaluation.event_id::text, episode.id::text,
       event.source_session, event.source_event_id, evaluation.symbol,
       evaluation.status::text, evaluation.reason, evaluation.block_number,
       evaluation.block_hash, evaluation.evidence, evaluation.evaluated_at,
       episode.closed_at
FROM paper_evaluations evaluation
JOIN paper_opportunity_episodes episode
  ON episode.id = evaluation.episode_id
 AND episode.latest_event_id = evaluation.event_id
 AND episode.strategy_version = evaluation.strategy_version
 AND episode.symbol = evaluation.symbol
 AND episode.status = 'closed'::paper_episode_status
 AND episode.close_reason = evaluation.reason
 AND episode.closed_at = evaluation.evaluated_at
JOIN raw_market_events event ON event.id = evaluation.event_id
WHERE evaluation.strategy_version = $1
  AND evaluation.symbol = $2
  AND event.source = 'lighter'
  AND event.kind = 'ticker'
  AND evaluation.status = 'declined'
  AND evaluation.reason IS NOT NULL
  AND evaluation.block_number IS NOT NULL
  AND evaluation.block_hash IS NOT NULL
  AND evaluation.evaluated_at >= $3 - interval '5 seconds'
  AND evaluation.evaluated_at <= $3
ORDER BY evaluation.evaluated_at, evaluation.id
LIMIT 64`, SourceStrategyVersion, Symbol, now)
	if err != nil {
		return nil, fmt.Errorf("query paper exits: %w", err)
	}
	defer rows.Close()
	exits := make([]PaperExit, 0, 8)
	for rows.Next() {
		var exit PaperExit
		var blockNumber int64
		var evidence []byte
		if err := rows.Scan(&exit.EvaluationID, &exit.EventID, &exit.EpisodeID,
			&exit.SourceSession, &exit.SourceEventID, &exit.Symbol, &exit.Status,
			&exit.Reason, &blockNumber, &exit.BlockHash, &evidence, &exit.EvaluatedAt,
			&exit.ClosedAt); err != nil {
			return nil, fmt.Errorf("scan paper exit: %w", err)
		}
		if blockNumber <= 0 {
			continue
		}
		exit.BlockNumber = uint64(blockNumber)
		exit.Evidence, err = DecodePaperEvidence(evidence)
		if err != nil {
			return nil, fmt.Errorf("decode paper exit %s: %w", exit.EvaluationID, err)
		}
		exits = append(exits, exit)
	}
	return exits, rows.Err()
}

type ProductSource interface {
	Accounts(context.Context, time.Time) ([]ProductAccount, error)
}

type PGResearchSource struct {
	pool *pgxpool.Pool
}

type PGProductSource struct {
	pool *pgxpool.Pool
}

func NewReadOnlyPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("parse read-only database URL")
	}
	config.MaxConns = 2
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET default_transaction_read_only = on")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func NewPGResearchSource(pool *pgxpool.Pool) (*PGResearchSource, error) {
	if pool == nil {
		return nil, errors.New("research pool is required")
	}
	return &PGResearchSource{pool: pool}, nil
}

func NewPGProductSource(pool *pgxpool.Pool) (*PGProductSource, error) {
	if pool == nil {
		return nil, errors.New("product pool is required")
	}
	return &PGProductSource{pool: pool}, nil
}

func (source *PGResearchSource) Candidates(ctx context.Context, now time.Time) ([]PaperCandidate, error) {
	rows, err := source.pool.Query(ctx, `
SELECT evaluation.id::text, evaluation.event_id::text, episode.id::text,
       event.source_session, event.source_event_id,
       evaluation.symbol, evaluation.status::text, evaluation.reason,
       evaluation.direction, evaluation.block_number, evaluation.block_hash,
       evaluation.gross_edge_ppm, evaluation.net_edge_ppm,
       evaluation.evidence, evaluation.evaluated_at
FROM paper_evaluations evaluation
JOIN paper_opportunity_episodes episode
  ON episode.id = evaluation.episode_id
 AND episode.first_event_id = evaluation.event_id
 AND episode.strategy_version = evaluation.strategy_version
 AND episode.symbol = evaluation.symbol
JOIN raw_market_events event ON event.id = evaluation.event_id
WHERE evaluation.strategy_version = $1
  AND evaluation.symbol = $2
  AND event.source = 'lighter'
  AND event.kind = 'ticker'
  AND evaluation.status = 'candidate'
  AND evaluation.direction = $3
  AND evaluation.evaluated_at >= $4 - interval '5 seconds'
  AND evaluation.evaluated_at <= $4
ORDER BY evaluation.evaluated_at, evaluation.id
LIMIT 64`, SourceStrategyVersion, Symbol, Direction, now)
	if err != nil {
		return nil, fmt.Errorf("query paper candidates: %w", err)
	}
	defer rows.Close()
	candidates := make([]PaperCandidate, 0, 8)
	for rows.Next() {
		var candidate PaperCandidate
		var blockNumber, grossEdge, netEdge int64
		var evidence []byte
		if err := rows.Scan(&candidate.EvaluationID, &candidate.EventID, &candidate.EpisodeID,
			&candidate.SourceSession, &candidate.SourceEventID,
			&candidate.Symbol, &candidate.Status, &candidate.Reason, &candidate.Direction,
			&blockNumber, &candidate.BlockHash, &grossEdge, &netEdge, &evidence,
			&candidate.EvaluatedAt); err != nil {
			return nil, fmt.Errorf("scan paper candidate: %w", err)
		}
		if blockNumber <= 0 || grossEdge < 0 || netEdge < 0 {
			continue
		}
		candidate.BlockNumber = uint64(blockNumber)
		candidate.GrossEdgePPM = uint64(grossEdge)
		candidate.NetEdgePPM = uint64(netEdge)
		candidate.Evidence, err = DecodePaperEvidence(evidence)
		if err != nil {
			return nil, fmt.Errorf("decode paper candidate %s: %w", candidate.EvaluationID, err)
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func (source *PGProductSource) Accounts(ctx context.Context, now time.Time) ([]ProductAccount, error) {
	rows, err := source.pool.Query(ctx, `
WITH complete AS (
    SELECT execution_account_id, snapshot_id, max(observed_at) AS observed_at
    FROM agent_readiness_evidence
    GROUP BY execution_account_id, snapshot_id
    HAVING count(DISTINCT check_name) = 8
), latest AS (
    SELECT DISTINCT ON (execution_account_id)
           execution_account_id, snapshot_id, observed_at
    FROM complete
    ORDER BY execution_account_id, observed_at DESC, snapshot_id DESC
)
SELECT account.id::text, agent.id::text, agent.status, account.status,
       account.strategy_version, account.strategy_manifest_sha256,
       registration.lighter_account_index, registration.lighter_api_key_index,
       lower(registration.robinhood_owner), lower(registration.robinhood_vault),
       lower(registration.robinhood_signer), registration.binding_sha256,
       registration.status, readiness.lighter_linked, readiness.lighter_funded,
       readiness.robinhood_deployed, readiness.robinhood_funded,
       readiness.user_gas_ready, readiness.execution_gas_ready,
       readiness.policy_active, readiness.reconciled,
       latest.observed_at, readiness.valid_until
FROM agents agent
JOIN execution_accounts account ON account.agent_id = agent.id
JOIN coordinator_account_registrations registration
  ON registration.execution_account_id = account.id
 AND registration.agent_id = agent.id
JOIN current_agent_readiness readiness ON readiness.execution_account_id = account.id
JOIN latest ON latest.execution_account_id = account.id
WHERE agent.mode = 'live'
  AND agent.status = 'running'
  AND account.status = 'ready'
  AND registration.status = 'registered'
  AND account.strategy_version = $1
  AND account.strategy_manifest_sha256 = $2
  AND readiness.lighter_linked
  AND readiness.lighter_funded
  AND readiness.robinhood_deployed
  AND readiness.robinhood_funded
  AND readiness.user_gas_ready
  AND readiness.execution_gas_ready
  AND readiness.policy_active
  AND readiness.reconciled
  AND latest.observed_at >= $3 - interval '5 seconds'
  AND latest.observed_at <= $3
  AND readiness.valid_until > $3
ORDER BY account.id`, schedulerStrategyVersion(), schedulerManifest(), now)
	if err != nil {
		return nil, fmt.Errorf("query live product accounts: %w", err)
	}
	defer rows.Close()
	accounts := make([]ProductAccount, 0, 8)
	for rows.Next() {
		var account ProductAccount
		var lighterAccount int64
		var lighterAPIKey int16
		if err := rows.Scan(&account.ExecutionAccountID, &account.AgentID, &account.Lifecycle,
			&account.AccountStatus, &account.StrategyVersion, &account.StrategyManifest,
			&lighterAccount, &lighterAPIKey, &account.RobinhoodOwner, &account.RobinhoodVault,
			&account.RobinhoodSigner, &account.BindingSHA256, &account.RegistrationStatus,
			&account.LighterLinked, &account.LighterFunded, &account.RobinhoodDeployed,
			&account.RobinhoodFunded, &account.UserGasReady, &account.ExecutionGasReady,
			&account.PolicyActive, &account.Reconciled, &account.ObservedAt,
			&account.ValidUntil); err != nil {
			return nil, fmt.Errorf("scan live product account: %w", err)
		}
		if lighterAccount <= 0 || lighterAPIKey < 0 || lighterAPIKey > 255 {
			continue
		}
		account.LighterAccount = uint64(lighterAccount)
		account.LighterAPIKey = uint8(lighterAPIKey)
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func schedulerStrategyVersion() string {
	return "basis-aapl-v1"
}

func schedulerManifest() string {
	return "7787f323c898f08bec51028ced5ee402f18f85da891515306ee330b2171c3902"
}
