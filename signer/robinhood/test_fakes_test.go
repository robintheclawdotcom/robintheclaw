package main

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type fakeJournal struct {
	ready         bool
	authNonces    map[string]struct{}
	existing      map[string]fakeExisting
	reservation   *fakeNonceReservation
	pending       []TransactionRecord
	quarantined   string
	submitted     string
	ambiguous     string
	receiptStatus string
	superseded    string
	finality      string
	deferred      string
	setSubmitted  error
	setAmbiguous  error
}

type fakeExisting struct {
	submission Submission
	digest     string
}

func (journal *fakeJournal) Ready(context.Context) bool { return journal.ready }

func (journal *fakeJournal) Existing(_ context.Context, requestID, digest string) (*Submission, error) {
	existing, ok := journal.existing[requestID]
	if !ok {
		return nil, nil
	}
	if existing.digest != digest {
		return nil, errors.New("request_id was reused with a different payload")
	}
	return &existing.submission, nil
}

func (*fakeJournal) Replacement(context.Context, string) (*replacementRecord, error) {
	return nil, nil
}

func (journal *fakeJournal) BeginNonce(_ context.Context, _ *big.Int, _ common.Address, observed uint64) (nonceReservation, error) {
	journal.reservation = &fakeNonceReservation{journal: journal, nonce: observed}
	return journal.reservation, nil
}

func (*fakeJournal) InsertReplacement(context.Context, signedRecord, *replacementRecord) error {
	return nil
}

func (journal *fakeJournal) SetSubmitted(ctx context.Context, requestID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if journal.setSubmitted != nil {
		return journal.setSubmitted
	}
	journal.submitted = requestID
	journal.setStatus(requestID, SubmissionSubmitted)
	return nil
}

func (journal *fakeJournal) SetAmbiguous(ctx context.Context, requestID, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if journal.setAmbiguous != nil {
		return journal.setAmbiguous
	}
	journal.ambiguous = requestID
	journal.setStatus(requestID, SubmissionAmbiguous)
	return nil
}

func (journal *fakeJournal) setStatus(requestID string, status SubmissionStatus) {
	existing := journal.existing[requestID]
	existing.submission.Status = status
	journal.existing[requestID] = existing
}

func (journal *fakeJournal) Pending(context.Context, int) ([]TransactionRecord, error) {
	return journal.pending, nil
}

func (journal *fakeJournal) SetReceipt(_ context.Context, _ string, status string, _ uint64, _ common.Hash, _ string) error {
	journal.receiptStatus = status
	return nil
}

func (journal *fakeJournal) SetSuperseded(_ context.Context, requestID, _ string, _ uint64) error {
	journal.superseded = requestID
	return nil
}

func (journal *fakeJournal) SetFinality(_ context.Context, _ string, status string) error {
	journal.finality = status
	return nil
}

func (journal *fakeJournal) DeferReconcile(_ context.Context, requestID string) error {
	journal.deferred = requestID
	return nil
}

func (journal *fakeJournal) Quarantine(_ context.Context, requestID, _ string) error {
	journal.quarantined = requestID
	return nil
}

func (journal *fakeJournal) ClaimAuthNonce(_ context.Context, nonce string, _ time.Time) error {
	if journal.authNonces == nil {
		journal.authNonces = make(map[string]struct{})
	}
	if _, exists := journal.authNonces[nonce]; exists {
		return context.Canceled
	}
	journal.authNonces[nonce] = struct{}{}
	return nil
}

type fakeNonceReservation struct {
	journal    *fakeJournal
	nonce      uint64
	record     signedRecord
	committed  bool
	rolledBack bool
}

func (reservation *fakeNonceReservation) Nonce() uint64 { return reservation.nonce }

func (reservation *fakeNonceReservation) Commit(_ context.Context, record signedRecord) error {
	reservation.record = record
	reservation.committed = true
	if reservation.journal.existing == nil {
		reservation.journal.existing = make(map[string]fakeExisting)
	}
	reservation.journal.existing[record.RequestID] = fakeExisting{
		submission: record.Submission,
		digest:     record.PayloadSHA256,
	}
	return nil
}

func (reservation *fakeNonceReservation) Rollback(context.Context) {
	if !reservation.committed {
		reservation.rolledBack = true
	}
}
