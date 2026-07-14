package exitquote

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"
)

var (
	ErrNoCandidate          = errors.New("no exit quote candidate")
	ErrPersistenceAmbiguous = errors.New("exit quote persistence is ambiguous")
)

type Store interface {
	Candidates(context.Context, time.Time, int) ([]Candidate, error)
	Persisted(context.Context, Candidate, PersistenceEvidence, time.Time) (bool, error)
}

type Publisher struct {
	store          Store
	quotes         QuoteClient
	quotePublicKey ed25519.PublicKey
	lighterMarket  uint32
	now            func() time.Time
}

func New(store Store, quotes QuoteClient, publicKey ed25519.PublicKey, lighterMarket uint32) (*Publisher, error) {
	if store == nil || quotes == nil || len(publicKey) != ed25519.PublicKeySize || lighterMarket == 0 {
		return nil, errors.New("invalid exit quote publisher dependencies")
	}
	return &Publisher{store: store, quotes: quotes, quotePublicKey: append(ed25519.PublicKey(nil), publicKey...),
		lighterMarket: lighterMarket, now: time.Now}, nil
}

func (publisher *Publisher) RunOnce(ctx context.Context) error {
	now := publisher.now().UTC()
	candidates, err := publisher.store.Candidates(ctx, now, 32)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return ErrNoCandidate
	}
	var failures []error
	for _, candidate := range candidates {
		if err := publisher.publish(ctx, candidate, now); err != nil {
			failures = append(failures, fmt.Errorf("%s/%s: %w", candidate.ExecutionAccountID, candidate.IntentID, err))
		}
	}
	return errors.Join(failures...)
}

func (publisher *Publisher) publish(ctx context.Context, candidate Candidate, now time.Time) error {
	request, err := quoteRequest(candidate, uint64(now.UnixMilli()))
	if err != nil {
		return err
	}
	quote, err := publisher.quotes.Quote(ctx, request)
	if err != nil {
		return err
	}
	evidence, err := evidenceFromQuote(candidate, request, quote, uint64(now.UnixMilli()),
		publisher.quotePublicKey, publisher.lighterMarket)
	if err != nil {
		return err
	}
	persisted, err := publisher.store.Persisted(ctx, candidate, evidence, now)
	if err != nil {
		return err
	}
	if !persisted {
		return ErrPersistenceAmbiguous
	}
	return nil
}
