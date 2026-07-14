package evaluation

import (
	"context"
	"errors"
	"time"
)

type Service struct {
	research ResearchSource
	product  ProductSource
	store    ApprovalStore
	config   Config
	now      func() time.Time
	onError  func(error)
}

func NewService(research ResearchSource, product ProductSource, store ApprovalStore, config Config) (*Service, error) {
	if research == nil || product == nil || store == nil || !config.Enabled ||
		config.MinimumNetEdgePPM == 0 || config.LighterMarket == 0 || config.ApprovalLifetime <= 0 {
		return nil, errors.New("complete live evaluation dependencies are required")
	}
	return &Service{
		research: research,
		product:  product,
		store:    store,
		config:   config,
		now:      func() time.Time { return time.Now().UTC() },
		onError:  func(error) {},
	}, nil
}

func (service *Service) SetErrorHandler(handler func(error)) {
	if handler != nil {
		service.onError = handler
	}
}

func (service *Service) RunOnce(ctx context.Context) (int, error) {
	now := service.now()
	candidates, err := service.research.Candidates(ctx, now)
	if err != nil {
		return 0, err
	}
	exits, err := service.research.Exits(ctx, now)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 && len(exits) == 0 {
		return 0, nil
	}
	accounts, err := service.product.Accounts(ctx, now)
	if err != nil {
		return 0, err
	}
	approved := 0
	var failures []error
	for _, candidate := range candidates {
		if err := candidate.Validate(now, service.config.MinimumNetEdgePPM); err != nil {
			failures = append(failures, err)
			continue
		}
		for _, account := range accounts {
			if err := account.Validate(now); err != nil {
				failures = append(failures, err)
				continue
			}
			inserted, err := service.store.Approve(ctx, candidate, account, now,
				service.config.ApprovalLifetime, service.config.MinimumNetEdgePPM, service.config.LighterMarket)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if inserted {
				approved++
			}
		}
	}
	for _, exit := range exits {
		if err := exit.Validate(now); err != nil {
			failures = append(failures, err)
			continue
		}
		for _, account := range accounts {
			if err := account.Validate(now); err != nil {
				failures = append(failures, err)
				continue
			}
			inserted, err := service.store.ApproveExit(ctx, exit, account, now,
				service.config.ApprovalLifetime, service.config.LighterMarket)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if inserted {
				approved++
			}
		}
	}
	return approved, errors.Join(failures...)
}

func (service *Service) Run(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if _, err := service.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				service.onError(err)
			}
			timer.Reset(service.config.PollInterval)
		}
	}
}
