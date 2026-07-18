package exitquote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PGStore struct{ pool *pgxpool.Pool }

func NewPGStore(pool *pgxpool.Pool) (*PGStore, error) {
	if pool == nil {
		return nil, errors.New("database pool is required")
	}
	return &PGStore{pool: pool}, nil
}

type sagaRecord struct {
	IntentID        string `json:"intent_id"`
	State           string `json:"state"`
	Version         uint64 `json:"version"`
	PerpFilledBase  uint64 `json:"perp_filled_base"`
	PerpUnwoundBase uint64 `json:"perp_unwound_base"`
	SpotReceivedRaw string `json:"spot_received_raw"`
}

func (store *PGStore) Candidates(ctx context.Context, now time.Time, limit int) ([]Candidate, error) {
	if limit < 1 || limit > 64 {
		return nil, errors.New("candidate limit is invalid")
	}
	rows, err := store.pool.Query(ctx, `
SELECT action.id, candidate.execution_account_id, intent.id,
       intent.payload #>> '{evidence,market_manifest}',
       candidate.target_strategy_manifest_sha256, intent.saga_version,
       intent.saga, action.kind, action.payload
FROM execution_exit_quote_candidates_v1 candidate
JOIN execution_actions action ON action.id = candidate.action_id
JOIN execution_intents intent ON intent.id = candidate.intent_id
WHERE action.available_at <= $1
  AND NOT (COALESCE(action.result, '{}'::jsonb) ?| ARRAY['send_authorized','signed','request','submission'])
  AND CASE
        WHEN action.payload #>> '{exit_authority,submission_deadline_ms}' ~ '^[0-9]{1,20}$'
        THEN (action.payload #>> '{exit_authority,submission_deadline_ms}')::numeric
        ELSE 0
      END <= (EXTRACT(EPOCH FROM $1) * 1000)::numeric + 1000
  AND NOT EXISTS (
      SELECT 1 FROM execution_market_quotes quote
      WHERE quote.source = 'execution-authority'
        AND quote.intent_id = intent.id
        AND quote.execution_account_id = intent.execution_account_id
        AND quote.market_manifest = intent.payload #>> '{evidence,market_manifest}'
        AND quote.strategy_manifest_sha256 = candidate.target_strategy_manifest_sha256
        AND quote.spot_unwind_amount_in = intent.saga->>'spot_received_raw'
        AND quote.exit_binding_version = 3
        AND quote.unwind_phase = CASE action.kind
              WHEN 'unwind_perp' THEN 'perp_and_spot' ELSE 'spot_only' END
        AND quote.perp_unwind_base_amount = CASE action.kind
              WHEN 'unwind_perp' THEN
                ((intent.saga->>'perp_filled_base')::numeric -
                 (intent.saga->>'perp_unwound_base')::numeric)::bigint
              ELSE 0 END
        AND quote.received_at <= $1
        AND quote.expires_at > $1 + interval '1 second'
  )
ORDER BY action.available_at, action.created_at, action.id
LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []Candidate
	for rows.Next() {
		var actionID, accountID, intentID, marketManifest, targetManifest, kind string
		var sagaVersion int64
		var sagaBody, payloadBody []byte
		if err := rows.Scan(&actionID, &accountID, &intentID, &marketManifest, &targetManifest, &sagaVersion, &sagaBody, &kind, &payloadBody); err != nil {
			return nil, err
		}
		var saga sagaRecord
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(sagaBody, &saga); err != nil || json.Unmarshal(payloadBody, &payload) != nil ||
			saga.IntentID != intentID || int64(saga.Version) != sagaVersion || saga.PerpUnwoundBase > saga.PerpFilledBase {
			return nil, errors.New("stored exit candidate is invalid")
		}
		remaining := saga.PerpFilledBase - saga.PerpUnwoundBase
		candidate := Candidate{ActionID: actionID, ExecutionAccountID: accountID, IntentID: intentID,
			MarketManifest: marketManifest, TargetStrategyManifestSHA256: targetManifest,
			SagaVersion: saga.Version, SpotAmount: saga.SpotReceivedRaw,
			PerpBaseAmount: remaining, Phase: PhasePerpAndSpot}
		if kind == "unwind_spot" {
			candidate.Phase = PhaseSpotOnly
		}
		if err := candidate.validate(); err != nil || !payloadMatches(payload, candidate, saga) {
			return nil, errors.New("stored exit candidate binding is invalid")
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func payloadMatches(payload map[string]json.RawMessage, candidate Candidate, saga sagaRecord) bool {
	if candidate.Phase == PhasePerpAndSpot {
		var filled, before uint64
		return json.Unmarshal(payload["filled_base"], &filled) == nil && filled == candidate.PerpBaseAmount &&
			json.Unmarshal(payload["unwound_before"], &before) == nil && before == saga.PerpUnwoundBase
	}
	var spot string
	return candidate.PerpBaseAmount == 0 && json.Unmarshal(payload["spot_amount"], &spot) == nil && spot == candidate.SpotAmount
}

func (store *PGStore) Persisted(ctx context.Context, candidate Candidate, evidence PersistenceEvidence, now time.Time) (bool, error) {
	if err := candidate.validate(); err != nil || !digestPattern.MatchString(evidence.PayloadSHA256) ||
		evidence.SourceSession == "" || evidence.SourceEventID == "" || evidence.MarkPrice == 0 ||
		evidence.UnwindPhase != candidate.Phase || evidence.PerpBaseAmount != candidate.PerpBaseAmount ||
		evidence.PerpLimitPrice == 0 ||
		!validPositiveUint(evidence.ExpectedUIMultiplier, "115792089237316195423570985008687907853269984665640564039457584007913129639935") ||
		!validPositiveUint(evidence.MinOracleRoundID, "1208925819614629174706175") {
		return false, errors.New("invalid persistence evidence")
	}
	var persisted bool
	err := store.pool.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM execution_market_quotes
  WHERE source = 'execution-authority' AND source_session = $1 AND source_event_id = $2
    AND payload_sha256 = $3 AND execution_account_id = $4 AND intent_id = $5
    AND market_manifest = $6 AND spot_unwind_amount_in = $7 AND mark_price = $8
    AND strategy_manifest_sha256 = $9
    AND expected_ui_multiplier = $10 AND min_oracle_round_id = $11
    AND (EXTRACT(EPOCH FROM received_at) * 1000)::bigint = $12
    AND submission_deadline_ms = $13 AND reconciliation_deadline_ms = $14
    AND exit_binding_version = 3 AND unwind_phase = $15
    AND perp_unwind_base_amount = $16 AND perp_unwind_limit_price = $17
    AND received_at <= $18 AND expires_at > $18
)`, evidence.SourceSession, evidence.SourceEventID, evidence.PayloadSHA256,
		candidate.ExecutionAccountID, candidate.IntentID, candidate.MarketManifest, candidate.SpotAmount,
		int64(evidence.MarkPrice), candidate.TargetStrategyManifestSHA256,
		evidence.ExpectedUIMultiplier, evidence.MinOracleRoundID,
		int64(evidence.ReceivedAtMS), int64(evidence.SubmissionDeadlineMS),
		int64(evidence.ReconciliationDeadlineMS), evidence.UnwindPhase, int64(evidence.PerpBaseAmount),
		int64(evidence.PerpLimitPrice), now).Scan(&persisted)
	if err != nil {
		return false, fmt.Errorf("verify persisted exit quote: %w", err)
	}
	return persisted, nil
}
