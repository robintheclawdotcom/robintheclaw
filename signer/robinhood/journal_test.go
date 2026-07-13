package main

import (
	"context"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5"
)

func TestJournalNonceAndIdempotency(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(ctx)
	migration, err := os.ReadFile("migrations/0001_journal.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}

	manifest := deploymentManifest{
		ChainID: "4663",
		Signer:  "0x0000000000000000000000000000000000000001",
		Vault:   "0x0000000000000000000000000000000000000002",
	}
	journal, err := openJournal(ctx, databaseURL, manifest, strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	address := common.HexToAddress("0x0000000000000000000000000000000000000001")
	reservation, err := journal.BeginNonce(ctx, big.NewInt(4663), address, 7)
	if err != nil || reservation.Nonce() != 7 {
		t.Fatalf("unexpected nonce reservation: %#v, %v", reservation, err)
	}
	reservation.Rollback(ctx)
	reservation, err = journal.BeginNonce(ctx, big.NewInt(4663), address, 7)
	if err != nil || reservation.Nonce() != 7 {
		t.Fatalf("rolled-back nonce was not reused: %#v, %v", reservation, err)
	}
	nonce := reservation.Nonce()

	digest := strings.Repeat("a", 64)
	record := signedRecord{
		Submission: Submission{
			RequestID: "request-1",
			IntentID:  "0x" + strings.Repeat("1", 64),
			TxHash:    "0x" + strings.Repeat("2", 64),
			Nonce:     nonce,
		},
		PayloadSHA256:  digest,
		Payload:        []byte(`{"request_id":"request-1"}`),
		SignedTx:       []byte{1, 2, 3},
		MaxFee:         big.NewInt(10),
		MaxPriorityFee: big.NewInt(1),
		GasLimit:       100_000,
		Evidence: VerificationEvidence{
			PrimaryBlock:   1,
			PrimaryHash:    common.HexToHash("0x" + strings.Repeat("4", 64)),
			SecondaryBlock: 1,
			SecondaryHash:  common.HexToHash("0x" + strings.Repeat("5", 64)),
		},
	}
	if err := reservation.Commit(ctx, record); err != nil {
		t.Fatal(err)
	}
	other, err := openJournal(ctx, databaseURL, manifest, strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if other.Ready(ctx) {
		t.Fatal("deployment rotation ignored a pending transaction from the same signer")
	}
	authNonce := strings.Repeat("n", 32)
	if err := journal.ClaimAuthNonce(ctx, authNonce, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := journal.ClaimAuthNonce(ctx, authNonce, time.Now().Add(time.Minute)); err == nil {
		t.Fatal("authorization nonce replay was accepted")
	}
	existing, err := journal.Existing(ctx, "request-1", digest)
	if err != nil || existing == nil || existing.Nonce != nonce {
		t.Fatalf("idempotent lookup failed: %#v, %v", existing, err)
	}
	if _, err := journal.Existing(ctx, "request-1", strings.Repeat("b", 64)); err == nil {
		t.Fatal("request ID reuse with another digest was accepted")
	}
	replacement, err := journal.Replacement(ctx, "request-1")
	if err != nil || replacement.Nonce != nonce {
		t.Fatalf("replacement target failed: %#v, %v", replacement, err)
	}
	replacementRecord := record
	replacementRecord.RequestID = "request-2"
	replacementRecord.PayloadSHA256 = strings.Repeat("b", 64)
	replacementRecord.Payload = []byte(`{"request_id":"request-2","replaces_request_id":"request-1"}`)
	replacementRecord.TxHash = "0x" + strings.Repeat("3", 64)
	replacementRecord.MaxFee = big.NewInt(12)
	replacementRecord.MaxPriorityFee = big.NewInt(2)
	if err := journal.InsertReplacement(ctx, replacementRecord, replacement); err != nil {
		t.Fatal(err)
	}
	pending, err := journal.Pending(ctx, 10)
	if err != nil || len(pending) != 2 {
		t.Fatalf("replacement family is not fully monitored: %#v, %v", pending, err)
	}
	if err := journal.DeferReconcile(ctx, "request-2"); err != nil {
		t.Fatal(err)
	}
	existing, err = journal.Existing(ctx, "request-1", digest)
	if err != nil || existing.Status != "replaced" {
		t.Fatalf("original transaction was not marked replaced: %#v, %v", existing, err)
	}
	if err := journal.InsertReplacement(ctx, replacementRecord, replacement); err == nil {
		t.Fatal("a replaced transaction accepted another replacement")
	}
	if err := journal.SetSuperseded(ctx, "request-1", record.IntentID, nonce); err != nil {
		t.Fatal(err)
	}
	existing, err = journal.Existing(ctx, "request-2", replacementRecord.PayloadSHA256)
	if err != nil || existing.Status != "superseded" {
		t.Fatalf("losing nonce variant was not superseded: %#v, %v", existing, err)
	}
}
