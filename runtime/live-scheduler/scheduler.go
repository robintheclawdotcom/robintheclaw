package scheduler

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var (
	ErrNoDispatch = errors.New("no scheduler dispatch")
	errHandled    = errors.New("dispatch was durably transitioned")
)

type Store interface {
	Claim(context.Context, time.Time, time.Duration) (*Dispatch, error)
	Eligible(context.Context, Dispatch) (bool, error)
	PrepareQuote(context.Context, Dispatch, string, uint64) error
	SaveQuote(context.Context, Dispatch, []byte, string) error
	SaveRunner(context.Context, Dispatch, []byte, string) error
	Complete(context.Context, Dispatch, []byte, string) error
	Ambiguous(context.Context, Dispatch, []byte, string) error
	Retry(context.Context, Dispatch, string) error
	Block(context.Context, Dispatch, string) error
}

type Scheduler struct {
	store          Store
	quotes         QuoteClient
	runner         RunnerClient
	quotePublicKey ed25519.PublicKey
	lease          time.Duration
	lighterMarket  uint32
	now            func() time.Time
}

func New(store Store, quotes QuoteClient, runner RunnerClient, quotePublicKey ed25519.PublicKey, lighterMarket uint32, lease time.Duration) (*Scheduler, error) {
	if store == nil || quotes == nil || runner == nil || len(quotePublicKey) != ed25519.PublicKeySize || lighterMarket == 0 || lease <= 0 {
		return nil, errors.New("invalid scheduler dependencies")
	}
	return &Scheduler{store: store, quotes: quotes, runner: runner, quotePublicKey: append(ed25519.PublicKey(nil), quotePublicKey...), lighterMarket: lighterMarket, lease: lease, now: time.Now}, nil
}

func (s *Scheduler) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return errors.New("poll interval must be positive")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		err := s.RunOnce(ctx)
		if err != nil && !errors.Is(err, ErrNoDispatch) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Scheduler) RunOnce(ctx context.Context) error {
	now := s.now().UTC()
	dispatch, err := s.store.Claim(ctx, now, s.lease)
	if err != nil {
		return err
	}
	if dispatch == nil {
		return ErrNoDispatch
	}
	if err := dispatch.validate(now); err != nil {
		return s.store.Block(ctx, *dispatch, err.Error())
	}
	if dispatch.AccountState.LighterMarketIndex != s.lighterMarket {
		return s.store.Block(ctx, *dispatch, "authoritative Lighter market identity mismatch")
	}
	eligible, err := s.store.Eligible(ctx, *dispatch)
	if err != nil {
		return err
	}
	if !eligible {
		return s.store.Block(ctx, *dispatch, "account is not registered, active, and promoted")
	}
	quote, err := s.resolveQuote(ctx, dispatch, now)
	if err != nil {
		if errors.Is(err, errHandled) {
			return nil
		}
		return err
	}
	eligible, err = s.store.Eligible(ctx, *dispatch)
	if err != nil {
		return err
	}
	if !eligible {
		return s.store.Block(ctx, *dispatch, "account became ineligible before strategy dispatch")
	}
	runnerBody, err := s.resolveRunnerBody(ctx, dispatch, quote)
	if err != nil {
		return err
	}
	response, err := s.runner.Run(ctx, runnerBody)
	if err != nil {
		var upstream *ResponseError
		if errors.As(err, &upstream) {
			switch {
			case upstream.Status == http.StatusBadGateway:
				return s.store.Ambiguous(ctx, *dispatch, upstream.Body, digest(upstream.Body))
			case upstream.Status >= 500:
				return s.store.Retry(ctx, *dispatch, "strategy runner unavailable")
			default:
				return s.store.Block(ctx, *dispatch, fmt.Sprintf("strategy runner rejected dispatch with HTTP %d", upstream.Status))
			}
		}
		return s.store.Retry(ctx, *dispatch, "strategy runner request failed")
	}
	if err := validateRunnerOutput(response, *dispatch); err != nil {
		return s.store.Block(ctx, *dispatch, err.Error())
	}
	return s.store.Complete(ctx, *dispatch, response, digest(response))
}

func (s *Scheduler) resolveQuote(ctx context.Context, dispatch *Dispatch, now time.Time) (QuoteBundle, error) {
	if dispatch.RequestID == "" {
		dispatch.RequestID = requestID(dispatch.EvaluationID, dispatch.ExecutionAccountID,
			dispatch.Evaluation.Action, dispatch.Evaluation.PairIntentID,
			dispatch.TargetStrategyManifestSHA256)
		dispatch.RequestedAtMS = uint64(now.UnixMilli())
		if err := s.store.PrepareQuote(ctx, *dispatch, dispatch.RequestID, dispatch.RequestedAtMS); err != nil {
			return QuoteBundle{}, err
		}
	}
	request := QuoteRequest{
		RequestID:                    dispatch.RequestID,
		ExecutionAccountID:           dispatch.ExecutionAccountID,
		SourceEvaluationID:           dispatch.EvaluationID,
		MarketManifest:               dispatch.Evaluation.MarketManifest,
		IntentID:                     dispatch.Evaluation.PairIntentID,
		TargetStrategyManifestSHA256: dispatch.TargetStrategyManifestSHA256,
		Action:                       dispatch.Evaluation.Action,
		RequestedAtMS:                dispatch.RequestedAtMS,
	}
	if len(dispatch.QuoteBody) != 0 {
		if digest(dispatch.QuoteBody) != dispatch.QuoteSHA256 {
			return QuoteBundle{}, transitioned(s.store.Block(ctx, *dispatch, "persisted quote digest mismatch"))
		}
		quote, err := validateQuote(dispatch.QuoteBody, request, s.quotePublicKey, s.lighterMarket, now)
		if err != nil {
			return QuoteBundle{}, transitioned(s.store.Block(ctx, *dispatch, err.Error()))
		}
		return quote, nil
	}
	requestBody, err := json.Marshal(request)
	if err != nil {
		return QuoteBundle{}, err
	}
	body, err := s.quotes.Quote(ctx, requestBody)
	if err != nil {
		var upstream *ResponseError
		if errors.As(err, &upstream) && upstream.Status < 500 {
			return QuoteBundle{}, transitioned(s.store.Block(ctx, *dispatch, fmt.Sprintf("quote authority rejected dispatch with HTTP %d", upstream.Status)))
		}
		return QuoteBundle{}, transitioned(s.store.Retry(ctx, *dispatch, "quote authority unavailable"))
	}
	quote, err := validateQuote(body, request, s.quotePublicKey, s.lighterMarket, now)
	if err != nil {
		return QuoteBundle{}, transitioned(s.store.Block(ctx, *dispatch, err.Error()))
	}
	dispatch.QuoteBody = append([]byte(nil), body...)
	dispatch.QuoteSHA256 = digest(body)
	if err := s.store.SaveQuote(ctx, *dispatch, body, dispatch.QuoteSHA256); err != nil {
		return QuoteBundle{}, err
	}
	return quote, nil
}

func transitioned(err error) error {
	if err != nil {
		return err
	}
	return errHandled
}

func (s *Scheduler) resolveRunnerBody(ctx context.Context, dispatch *Dispatch, _ QuoteBundle) ([]byte, error) {
	if len(dispatch.RunnerBody) != 0 {
		if digest(dispatch.RunnerBody) != dispatch.RunnerSHA256 {
			_ = s.store.Block(ctx, *dispatch, "persisted runner request digest mismatch")
			return nil, errors.New("persisted runner request digest mismatch")
		}
		return append([]byte(nil), dispatch.RunnerBody...), nil
	}
	body, err := json.Marshal(RunRequest{
		Evaluation:   dispatch.Evaluation,
		Readiness:    dispatch.Readiness,
		AccountState: dispatch.AccountState,
		Quotes:       json.RawMessage(dispatch.QuoteBody),
		OpenEpisode:  dispatch.OpenEpisode,
	})
	if err != nil {
		return nil, err
	}
	dispatch.RunnerBody = append([]byte(nil), body...)
	dispatch.RunnerSHA256 = digest(body)
	if err := s.store.SaveRunner(ctx, *dispatch, body, dispatch.RunnerSHA256); err != nil {
		return nil, err
	}
	return body, nil
}
