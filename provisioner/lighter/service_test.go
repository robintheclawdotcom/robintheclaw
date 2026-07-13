package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	testExecutionID = "11111111-1111-4111-8111-111111111111"
	testOwner       = "0x1111111111111111111111111111111111111111"
)

func newTestService() (*service, *memoryStore, *fakeLighter) {
	store := newMemoryStore()
	kms := &fakeKMS{}
	vault := newEnvelope(kms, "alias/lighter")
	vault.rand = bytes.NewReader([]byte(strings.Repeat("r", 4096)))
	lighter := &fakeLighter{recoveredOwner: testOwner}
	fixedNow := time.Unix(2_000_000_000, 0)
	return &service{
		store:    store,
		envelope: vault,
		lighter:  lighter,
		ttl:      10 * time.Minute,
		now:      func() time.Time { return fixedNow },
	}, store, lighter
}

func TestConfirmBlocksRegisteredKeyMismatch(t *testing.T) {
	service, store, lighter := newTestService()
	link, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		AccountIndex:       42,
		APIKeyIndex:        3,
		Nonce:              7,
	})
	if err != nil {
		t.Fatal(err)
	}
	lighter.registered = "0x" + strings.Repeat("ff", 40)
	if _, _, err := service.confirm(context.Background(), confirmRequest{
		ExecutionAccountID: testExecutionID,
		LinkID:             link.LinkID,
		L1Signature:        validTestSignature(),
	}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected key mismatch, got %v", err)
	}
	record, err := store.Get(context.Background(), testExecutionID, link.LinkID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != statusBlocked {
		t.Fatalf("status = %s", record.Status)
	}
	if _, err := store.Active(context.Background(), testExecutionID); err == nil {
		t.Fatal("mismatched credential became active")
	}
}

func TestRotationBlocksAuthUntilNewCredentialVerifies(t *testing.T) {
	service, store, lighter := newTestService()
	first, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		AccountIndex:       42,
		APIKeyIndex:        3,
		Nonce:              7,
	})
	if err != nil {
		t.Fatal(err)
	}
	lighter.registered = first.PublicKey
	if _, linked, err := service.confirm(context.Background(), confirmRequest{
		ExecutionAccountID: testExecutionID,
		LinkID:             first.LinkID,
		L1Signature:        validTestSignature(),
	}); err != nil || !linked {
		t.Fatalf("link first credential: linked=%v err=%v", linked, err)
	}
	if _, err := service.authToken(context.Background(), authTokenRequest{
		ExecutionAccountID: testExecutionID,
		ExpiresAtUnix:      service.now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("active credential rejected: %v", err)
	}

	second, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		AccountIndex:       42,
		APIKeyIndex:        3,
		Nonce:              8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.CredentialVersion != 2 {
		t.Fatalf("version = %d", second.CredentialVersion)
	}
	if _, err := service.authToken(context.Background(), authTokenRequest{
		ExecutionAccountID: testExecutionID,
		ExpiresAtUnix:      service.now().Add(time.Hour).Unix(),
	}); err == nil {
		t.Fatal("auth token issued during rotation")
	}

	lighter.recoveredOwner = "0x2222222222222222222222222222222222222222"
	if _, _, err := service.confirm(context.Background(), confirmRequest{
		ExecutionAccountID: testExecutionID,
		LinkID:             second.LinkID,
		L1Signature:        validTestSignature(),
	}); err == nil {
		t.Fatal("owner mismatch accepted")
	}
	if _, err := store.Active(context.Background(), testExecutionID); err == nil {
		t.Fatal("old credential reactivated after failed rotation")
	}
}

func TestAmbiguousSubmissionIsNeverRebroadcast(t *testing.T) {
	service, store, lighter := newTestService()
	link, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		AccountIndex:       42,
		APIKeyIndex:        3,
		Nonce:              7,
	})
	if err != nil {
		t.Fatal(err)
	}
	lighter.broadcastErr = errAmbiguousSubmission
	request := confirmRequest{
		ExecutionAccountID: testExecutionID,
		LinkID:             link.LinkID,
		L1Signature:        validTestSignature(),
	}
	if _, linked, err := service.confirm(context.Background(), request); err != nil || linked {
		t.Fatalf("first confirm: linked=%v err=%v", linked, err)
	}
	lighter.broadcastErr = nil
	if _, linked, err := service.confirm(context.Background(), request); err != nil || linked {
		t.Fatalf("second confirm: linked=%v err=%v", linked, err)
	}
	if lighter.broadcasts != 1 {
		t.Fatalf("broadcasts = %d", lighter.broadcasts)
	}
	if _, err := store.Active(context.Background(), testExecutionID); err == nil {
		t.Fatal("ambiguous credential became active")
	}
}

func TestSubmissionResponseMismatchBlocksCredential(t *testing.T) {
	service, store, lighter := newTestService()
	link, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		AccountIndex:       42,
		APIKeyIndex:        3,
		Nonce:              7,
	})
	if err != nil {
		t.Fatal(err)
	}
	lighter.broadcastErr = errLighterHashMismatch
	if _, _, err := service.confirm(context.Background(), confirmRequest{
		ExecutionAccountID: testExecutionID,
		LinkID:             link.LinkID,
		L1Signature:        validTestSignature(),
	}); !errors.Is(err, errLighterHashMismatch) {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
	record, err := store.Get(context.Background(), testExecutionID, link.LinkID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != statusBlocked {
		t.Fatalf("status = %s", record.Status)
	}
}

func TestPrepareRetryReturnsExistingAssociation(t *testing.T) {
	service, _, lighter := newTestService()
	request := prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		AccountIndex:       42,
		APIKeyIndex:        3,
		Nonce:              7,
	}
	first, err := service.prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.LinkID != first.LinkID || second.MessageToSign != first.MessageToSign || second.PublicKey != first.PublicKey {
		t.Fatalf("retry returned a different association: first=%+v second=%+v", first, second)
	}
	if lighter.generated != 1 {
		t.Fatalf("generated credentials = %d", lighter.generated)
	}
	request.Nonce++
	if _, err := service.prepare(context.Background(), request); !errors.Is(err, errRotationOpen) {
		t.Fatalf("changed retry returned %v", err)
	}
	request.Nonce--
	request.OwnerAddress = "0x2222222222222222222222222222222222222222"
	if _, err := service.prepare(context.Background(), request); !errors.Is(err, errBindingMismatch) {
		t.Fatalf("owner substitution returned %v", err)
	}
}

func TestSigningStopsDuringRotationAndUsesNewCredentialAfterActivation(t *testing.T) {
	service, _, lighter := newTestService()
	first, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		AccountIndex:       42,
		APIKeyIndex:        3,
		Nonce:              7,
	})
	if err != nil {
		t.Fatal(err)
	}
	lighter.registered = first.PublicKey
	if _, linked, err := service.confirm(context.Background(), confirmRequest{
		ExecutionAccountID: testExecutionID,
		LinkID:             first.LinkID,
		L1Signature:        validTestSignature(),
	}); err != nil || !linked {
		t.Fatalf("activate first credential: linked=%v err=%v", linked, err)
	}
	request := createOrderRequest{
		ExecutionAccountID: testExecutionID,
		IntentID:           "intent-001",
		TransactOptions:    transactOptions{Nonce: 9, ExpiresAtMS: service.now().Add(time.Minute).UnixMilli()},
	}
	signed, err := service.signCreateOrder(context.Background(), request)
	if err != nil || signed.CredentialVersion != 1 {
		t.Fatalf("sign with first credential: version=%d err=%v", signed.CredentialVersion, err)
	}

	second, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		AccountIndex:       42,
		APIKeyIndex:        3,
		Nonce:              8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.signCreateOrder(context.Background(), request); err == nil {
		t.Fatal("transaction signed while rotation was pending")
	}
	lighter.registered = second.PublicKey
	if _, linked, err := service.confirm(context.Background(), confirmRequest{
		ExecutionAccountID: testExecutionID,
		LinkID:             second.LinkID,
		L1Signature:        validTestSignature(),
	}); err != nil || !linked {
		t.Fatalf("activate rotated credential: linked=%v err=%v", linked, err)
	}
	request.TransactOptions.Nonce++
	signed, err = service.signCreateOrder(context.Background(), request)
	if err != nil || signed.CredentialVersion != 2 {
		t.Fatalf("sign with rotated credential: version=%d err=%v", signed.CredentialVersion, err)
	}
}

func TestSigningRejectsCrossAccountSubstitution(t *testing.T) {
	service, _, lighter := newTestService()
	link, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		AccountIndex:       42,
		APIKeyIndex:        3,
		Nonce:              7,
	})
	if err != nil {
		t.Fatal(err)
	}
	lighter.registered = link.PublicKey
	if _, linked, err := service.confirm(context.Background(), confirmRequest{
		ExecutionAccountID: testExecutionID,
		LinkID:             link.LinkID,
		L1Signature:        validTestSignature(),
	}); err != nil || !linked {
		t.Fatalf("activate credential: linked=%v err=%v", linked, err)
	}
	_, err = service.signCreateOrder(context.Background(), createOrderRequest{
		ExecutionAccountID: "22222222-2222-4222-8222-222222222222",
		IntentID:           "intent-002",
		TransactOptions:    transactOptions{Nonce: 1, ExpiresAtMS: service.now().Add(time.Minute).UnixMilli()},
	})
	if err == nil || !strings.Contains(err.Error(), "no active") {
		t.Fatalf("cross-account substitution returned %v", err)
	}
}
