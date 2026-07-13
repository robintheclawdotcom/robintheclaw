package publisher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type Service struct {
	config      Config
	accounts    AccountSource
	lighter     lighterCollector
	robinhood   robinhoodCollector
	coordinator snapshotClient
	application snapshotClient
	session     string
	mu          sync.Mutex
	sequences   map[string]int64
	ready       atomic.Bool
	lastSuccess atomic.Int64
	metrics     *publisherMetrics
}

type lighterCollector interface {
	Collect(context.Context, string, LighterBinding) (LighterObservation, error)
}

type robinhoodCollector interface {
	Collect(context.Context, RobinhoodBinding) (RobinhoodObservation, error)
}

type snapshotClient interface {
	Post(context.Context, string, []byte) error
}

func NewService(config Config, client *http.Client) (*Service, error) {
	metrics := newPublisherMetrics(config.Environment)
	if !config.Enabled {
		return &Service{config: config, metrics: metrics}, nil
	}
	startup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	accounts, err := NewPGAccountSource(startup, config)
	if err != nil {
		return nil, err
	}
	lighter, err := NewLighterClient(config.LighterBridge, client)
	if err != nil {
		accounts.Close()
		return nil, err
	}
	robinhood, err := NewRobinhoodClient(config.PrimaryRPCURL, config.SecondaryRPCURL, client)
	if err != nil {
		accounts.Close()
		return nil, err
	}
	coordinator, err := NewSignedClient(config.Coordinator.URL, config.Coordinator.Caller, config.Coordinator.HMACKey, client)
	if err != nil {
		accounts.Close()
		return nil, err
	}
	application, err := NewSignedClient(config.Application.URL, config.Application.Caller, config.Application.HMACKey, client)
	if err != nil {
		accounts.Close()
		return nil, err
	}
	if coordinator.key == application.key || coordinator.key == lighter.bridge.key || application.key == lighter.bridge.key {
		accounts.Close()
		return nil, errors.New("coordinator and readiness HMAC keys must be distinct")
	}
	sessionBytes := make([]byte, 16)
	if _, err := rand.Read(sessionBytes); err != nil {
		return nil, err
	}
	return &Service{
		config: config, accounts: accounts, lighter: lighter, robinhood: robinhood, coordinator: coordinator,
		application: application, session: hex.EncodeToString(sessionBytes), sequences: make(map[string]int64),
		metrics: metrics,
	}, nil
}

func (s *Service) Run(ctx context.Context) error {
	if !s.config.Enabled {
		<-ctx.Done()
		return ctx.Err()
	}
	ticker := time.NewTicker(s.config.PollInterval)
	defer ticker.Stop()
	for {
		err := s.RunOnce(ctx)
		if errors.Is(err, ErrRateLimited) {
			s.ready.Store(false)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(60 * time.Second):
			}
		} else if err != nil {
			s.ready.Store(false)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) RunOnce(ctx context.Context) error {
	if !s.config.Enabled {
		return errors.New("publisher disabled")
	}
	discovery, err := s.accounts.List(ctx)
	if err != nil {
		s.ready.Store(false)
		return err
	}
	s.metrics.BeginCycle(discovery.Accounts)
	if len(discovery.Accounts) == 0 {
		s.ready.Store(false)
		return errors.New("no active execution accounts")
	}
	allSucceeded := len(discovery.RejectedIDs) == 0
	for _, account := range discovery.Accounts {
		accountCtx, cancel := context.WithTimeout(ctx, 4500*time.Millisecond)
		type lighterCollection struct {
			observation LighterObservation
			err         error
		}
		type robinhoodCollection struct {
			observation RobinhoodObservation
			err         error
		}
		lighterResultCh := make(chan lighterCollection, 1)
		robinhoodResultCh := make(chan robinhoodCollection, 1)
		go func() {
			observation, err := s.lighter.Collect(accountCtx, account.ExecutionAccountID, account.Lighter)
			lighterResultCh <- lighterCollection{observation: observation, err: err}
		}()
		go func() {
			observation, err := s.robinhood.Collect(accountCtx, account.Robinhood)
			robinhoodResultCh <- robinhoodCollection{observation: observation, err: err}
		}()
		lighterResult := <-lighterResultCh
		robinhoodResult := <-robinhoodResultCh
		if lighterResult.err != nil {
			s.metrics.SourceFailure(account.ExecutionAccountID, "lighter")
			allSucceeded = false
			if errors.Is(lighterResult.err, ErrRateLimited) {
				cancel()
				return ErrRateLimited
			}
			if readinessErr := s.publishBlockedReadiness(accountCtx, account.ReadinessAccountID, "lighter-auth-unavailable"); readinessErr != nil {
				cancel()
				return readinessErr
			}
			cancel()
			continue
		}
		if robinhoodResult.err != nil {
			s.metrics.SourceFailure(account.ExecutionAccountID, "robinhood")
			allSucceeded = false
			if errors.Is(robinhoodResult.err, ErrRateLimited) {
				cancel()
				return ErrRateLimited
			}
			if readinessErr := s.publishBlockedReadiness(accountCtx, account.ReadinessAccountID, "robinhood-finality-unavailable"); readinessErr != nil {
				cancel()
				return readinessErr
			}
			cancel()
			continue
		}
		s.metrics.Observe(account, lighterResult.observation, robinhoodResult.observation)
		if err := s.publishAccount(accountCtx, account, lighterResult.observation, robinhoodResult.observation); err != nil {
			allSucceeded = false
			if errors.Is(err, ErrRateLimited) {
				cancel()
				return ErrRateLimited
			}
		}
		cancel()
	}
	s.ready.Store(allSucceeded)
	if allSucceeded {
		s.lastSuccess.Store(time.Now().Unix())
		return nil
	}
	return errors.New("one or more account observations failed")
}

func (s *Service) publishAccount(ctx context.Context, account AccountBinding, lighter LighterObservation, robinhood RobinhoodObservation) error {
	now := time.Now().UTC()
	if !fresh(lighter.ObservedAt, now) || !fresh(robinhood.ObservedAt, now) {
		return errors.New("upstream evidence expired before publication")
	}
	lighterSnapshot := CoordinatorSnapshot{
		ExecutionAccountID: account.ExecutionAccountID, Source: "lighter-auth", SourceSession: s.session,
		SourceSequence: s.nextSequence(account.ExecutionAccountID + ":lighter"),
		Payload: LighterPayload{
			AccountIndex: lighter.AccountIndex, APIKeyIndex: lighter.APIKeyIndex,
			NonceAligned: lighter.Nonce == lighter.ExpectedNonce, NoUnknownOrders: lighter.NoUnknownOrders,
			NoUnknownPositions: lighter.NoUnknownPositions, CollateralReady: lighter.CollateralReady,
			MaintenanceMarginRatioMicros: lighter.MaintenanceMarginRatioMicros, Flat: lighter.Flat,
		},
		ObservedAtMS: lighter.ObservedAt.UnixMilli(), ReceivedAtMS: now.UnixMilli(), ExpiresAtMS: now.Add(5 * time.Second).UnixMilli(),
	}
	robinhoodSnapshot := CoordinatorSnapshot{
		ExecutionAccountID: account.ExecutionAccountID, Source: "robinhood-chain", SourceSession: s.session,
		SourceSequence: s.nextSequence(account.ExecutionAccountID + ":robinhood"),
		Payload: RobinhoodPayload{
			VaultAddress: robinhood.Vault, SignerAddress: robinhood.Signer, FundingReady: robinhood.FundingReady,
			WiringVerified: robinhood.WiringVerified, FinalityHealthy: robinhood.FinalityHealthy,
			Flat: robinhood.Flat, OwnerAddress: robinhood.Owner, AgentEnabled: robinhood.AgentEnabled,
			RiskMode: robinhood.RiskMode, SettlementBalanceRaw: robinhood.SettlementBalanceRaw,
		},
		ObservedAtMS: robinhood.ObservedAt.UnixMilli(), ReceivedAtMS: now.UnixMilli(), ExpiresAtMS: now.Add(5 * time.Second).UnixMilli(),
	}
	for _, snapshot := range []CoordinatorSnapshot{lighterSnapshot, robinhoodSnapshot} {
		body, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		if err := s.coordinator.Post(ctx, "/v1/account-snapshots", body); err != nil {
			return err
		}
	}
	return s.publishReadiness(ctx, account.ReadinessAccountID, account.PolicyActive, lighter, robinhood)
}

func (s *Service) publishReadiness(ctx context.Context, accountID string, policyActive bool, lighter LighterObservation, robinhood RobinhoodObservation) error {
	if accountID == "" {
		return nil
	}
	now := time.Now().UTC()
	digest := EvidenceDigest(struct {
		Lighter   LighterObservation
		Robinhood RobinhoodObservation
	}{lighter, robinhood})
	reconciled := lighter.RESTReconstructed && lighter.Nonce == lighter.ExpectedNonce &&
		lighter.NoUnknownOrders && lighter.NoUnknownPositions && robinhood.WiringVerified && robinhood.FinalityHealthy
	checks := []ReadinessEvidence{
		readiness("execution_gas_ready", robinhood.SignerGasReady, "robinhood-dual-rpc", digest, now),
		readiness("lighter_funded", lighter.CollateralReady, "lighter-auth-rest", digest, now),
		readiness("lighter_linked", lighter.RESTReconstructed && lighter.Nonce == lighter.ExpectedNonce, "lighter-auth-rest", digest, now),
		readiness("policy_active", policyActive, "coordinator-account-policy", EvidenceDigest(struct {
			ExecutionAccountID string
			Active             bool
		}{accountID, policyActive}), now),
		readiness("reconciled", reconciled, "account-state-reconciler", digest, now),
		readiness("robinhood_deployed", robinhood.WiringVerified && robinhood.FinalityHealthy, "robinhood-dual-rpc", digest, now),
		readiness("robinhood_funded", robinhood.FundingReady, "robinhood-dual-rpc", digest, now),
		readiness("user_gas_ready", robinhood.OwnerGasReady, "robinhood-dual-rpc", digest, now),
	}
	body, err := json.Marshal(ReadinessSnapshot{ExecutionAccountID: accountID, Evidence: checks})
	if err != nil {
		return err
	}
	return s.application.Post(ctx, "/internal/v1/readiness", body)
}

func (s *Service) publishBlockedReadiness(ctx context.Context, accountID, source string) error {
	if accountID == "" {
		return nil
	}
	now := time.Now().UTC()
	digest := EvidenceDigest(source)
	checks := make([]ReadinessEvidence, 0, 8)
	for _, name := range []string{"execution_gas_ready", "lighter_funded", "lighter_linked", "policy_active", "reconciled", "robinhood_deployed", "robinhood_funded", "user_gas_ready"} {
		checks = append(checks, readiness(name, false, source, digest, now))
	}
	body, err := json.Marshal(ReadinessSnapshot{ExecutionAccountID: accountID, Evidence: checks})
	if err != nil {
		return err
	}
	return s.application.Post(ctx, "/internal/v1/readiness", body)
}

func readiness(name string, ready bool, source, digest string, now time.Time) ReadinessEvidence {
	return ReadinessEvidence{CheckName: name, Ready: ready, Source: source, EvidenceDigest: digest, ObservedAt: now, ExpiresAt: now.Add(5 * time.Second)}
}

func (s *Service) nextSequence(key string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequences[key]++
	return s.sequences[key]
}

func (s *Service) HealthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", s.metrics.Handler())
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /readyz", func(writer http.ResponseWriter, _ *http.Request) {
		if !s.config.Enabled || !s.ready.Load() || time.Now().Unix()-s.lastSuccess.Load() > int64((2*s.config.PollInterval)/time.Second) {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"status":"not_ready"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"status":"ready"}`))
	})
	return mux
}

func (s *Service) String() string {
	return fmt.Sprintf("account publisher enabled=%t accounts=dynamic", s.config.Enabled)
}

func (s *Service) Close() {
	if s.accounts != nil {
		s.accounts.Close()
	}
}
