package main

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type fakeJournal struct {
	ready         bool
	authNonces    map[string]struct{}
	reservation   *fakeNonceReservation
	pending       []TransactionRecord
	quarantined   string
	submitted     string
	ambiguous     string
	receiptStatus string
	superseded    string
	finality      string
	deferred      string
}

func (journal *fakeJournal) Ready(context.Context) bool { return journal.ready }

func (*fakeJournal) Existing(context.Context, string, string) (*Submission, error) {
	return nil, nil
}

func (*fakeJournal) Replacement(context.Context, string) (*replacementRecord, error) {
	return nil, nil
}

func (journal *fakeJournal) BeginNonce(_ context.Context, _ *big.Int, _ common.Address, observed uint64) (nonceReservation, error) {
	journal.reservation = &fakeNonceReservation{nonce: observed}
	return journal.reservation, nil
}

func (*fakeJournal) InsertReplacement(context.Context, signedRecord, *replacementRecord) error {
	return nil
}

func (journal *fakeJournal) SetSubmitted(_ context.Context, requestID string) error {
	journal.submitted = requestID
	return nil
}

func (journal *fakeJournal) SetAmbiguous(_ context.Context, requestID, _ string) error {
	journal.ambiguous = requestID
	return nil
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
	nonce      uint64
	record     signedRecord
	committed  bool
	rolledBack bool
}

func (reservation *fakeNonceReservation) Nonce() uint64 { return reservation.nonce }

func (reservation *fakeNonceReservation) Commit(_ context.Context, record signedRecord) error {
	reservation.record = record
	reservation.committed = true
	return nil
}

func (reservation *fakeNonceReservation) Rollback(context.Context) {
	if !reservation.committed {
		reservation.rolledBack = true
	}
}
