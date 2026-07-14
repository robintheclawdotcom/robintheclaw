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
		store:               store,
		envelope:            vault,
		lighter:             lighter,
		ttl:                 10 * time.Minute,
		now:                 func() time.Time { return fixedNow },
		publisherMarketID:   5,
		marketBaseDecimals:  4,
		marketPriceDecimals: 2,
	}, store, lighter
}

func TestPrepareRejectsReservedAPIKeyIndexes(t *testing.T) {
	service, _, _ := newTestService()
	for _, index := range []uint8{0, 1, 2, 3} {
		_, err := service.prepare(context.Background(), prepareRequest{
			ExecutionAccountID: testExecutionID,
			OwnerAddress:       testOwner,
			APIKeyIndex:        index,
		})
		if err == nil || !strings.Contains(err.Error(), "between 4 and 254") {
			t.Fatalf("reserved API key index %d returned %v", index, err)
		}
	}
}

func TestPrepareDiscoversAccountAndNonceBeforeReservation(t *testing.T) {
	service, store, lighter := newTestService()
	lighter.discoveredAccount = 73
	lighter.nextNonce = 11
	link, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		APIKeyIndex:        4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if link.AccountIndex != 73 || link.APIKeyIndex != 4 || lighter.discoveryCalls != 1 || lighter.nonceCalls != 1 {
		t.Fatalf("link=%+v discoveryCalls=%d nonceCalls=%d", link, lighter.discoveryCalls, lighter.nonceCalls)
	}
	record, err := store.Get(context.Background(), testExecutionID, link.LinkID)
	if err != nil {
		t.Fatal(err)
	}
	if record.AccountIndex != 73 || record.ChangeNonce != 11 {
		t.Fatalf("reserved account=%d nonce=%d", record.AccountIndex, record.ChangeNonce)
	}
}

func TestPrepareDoesNotReserveWhenDiscoveryFails(t *testing.T) {
	service, store, lighter := newTestService()
	lighter.discoveryErr = errNoEmptySubaccount
	_, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		APIKeyIndex:        4,
	})
	if !errors.Is(err, errNoEmptySubaccount) {
		t.Fatalf("discovery error = %v", err)
	}
	if lighter.nonceCalls != 0 || lighter.generated != 0 {
		t.Fatalf("nonceCalls=%d generated=%d", lighter.nonceCalls, lighter.generated)
	}
	if _, err := store.Latest(context.Background(), testExecutionID); !errors.Is(err, errNotFound) {
		t.Fatalf("failed discovery reserved a credential: %v", err)
	}
}

func TestPrepareCannotReuseSubaccountAcrossExecutionAccounts(t *testing.T) {
	service, _, _ := newTestService()
	if _, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		APIKeyIndex:        4,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: "22222222-2222-4222-8222-222222222222",
		OwnerAddress:       testOwner,
		APIKeyIndex:        4,
	})
	if !errors.Is(err, errAccountBound) {
		t.Fatalf("account reuse error = %v", err)
	}
}

func TestConfirmBlocksRegisteredKeyMismatch(t *testing.T) {
	service, store, lighter := newTestService()
	link, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		APIKeyIndex:        4,
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

func TestRotationBlocksCredentialUseUntilNewCredentialVerifies(t *testing.T) {
	service, store, lighter := newTestService()
	first, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		APIKeyIndex:        4,
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
	if _, err := service.publisherAccountState(context.Background(), publisherAccountStateRequest{ExecutionAccountID: testExecutionID}); err != nil {
		t.Fatalf("active credential rejected: %v", err)
	}

	lighter.nextNonce = 8
	second, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		APIKeyIndex:        4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.CredentialVersion != 2 {
		t.Fatalf("version = %d", second.CredentialVersion)
	}
	if _, err := service.publisherAccountState(context.Background(), publisherAccountStateRequest{ExecutionAccountID: testExecutionID}); err == nil {
		t.Fatal("credential used during rotation")
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
		APIKeyIndex:        4,
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
		APIKeyIndex:        4,
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
		APIKeyIndex:        4,
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
	lighter.discoveryErr = errNoEmptySubaccount
	if retry, err := service.prepare(context.Background(), request); err != nil || retry.LinkID != first.LinkID {
		t.Fatalf("state change broke idempotent retry: link=%+v err=%v", retry, err)
	}
	lighter.discoveryErr = nil
	lighter.nextNonce = 8
	if retry, err := service.prepare(context.Background(), request); err != nil || retry.LinkID != first.LinkID {
		t.Fatalf("nonce change broke idempotent retry: link=%+v err=%v", retry, err)
	}
	lighter.nextNonce = 7
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
		APIKeyIndex:        4,
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
		MarketIndex:        5,
		ClientOrderID:      1,
		BaseAmount:         10_000,
		Price:              2_500,
		IsAsk:              true,
		TransactOptions:    transactOptions{Nonce: 8, ExpiresAtMS: service.now().Add(time.Minute).UnixMilli()},
	}
	signed, err := service.signCreateOrder(context.Background(), request)
	if err != nil || signed.CredentialVersion != 1 {
		t.Fatalf("sign with first credential: version=%d err=%v", signed.CredentialVersion, err)
	}
	retry, err := service.signCreateOrder(context.Background(), request)
	if err != nil || retry.TxHash != signed.TxHash || retry.CredentialVersion != signed.CredentialVersion {
		t.Fatalf("exact signing retry was not idempotent: retry=%+v err=%v", retry, err)
	}
	substituted := request
	substituted.Price--
	if _, err := service.signCreateOrder(context.Background(), substituted); err == nil || !strings.Contains(err.Error(), "another request") {
		t.Fatalf("same nonce with substituted payload returned %v", err)
	}
	substituted = request
	substituted.IntentID = "intent-999"
	if _, err := service.signCreateOrder(context.Background(), substituted); err == nil || !strings.Contains(err.Error(), "another request") {
		t.Fatalf("same nonce with substituted intent returned %v", err)
	}
	request.TransactOptions.Nonce = 10
	if _, err := service.signCreateOrder(context.Background(), request); err == nil || !strings.Contains(err.Error(), "must equal 9") {
		t.Fatalf("skipped nonce returned %v", err)
	}
	request.TransactOptions.Nonce = 8

	lighter.nextNonce = 8
	second, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       testOwner,
		APIKeyIndex:        4,
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
		APIKeyIndex:        4,
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
		MarketIndex:        5,
		ClientOrderID:      1,
		BaseAmount:         10_000,
		Price:              2_500,
		IsAsk:              true,
		TransactOptions:    transactOptions{Nonce: 1, ExpiresAtMS: service.now().Add(time.Minute).UnixMilli()},
	})
	if err == nil || !strings.Contains(err.Error(), "no active") {
		t.Fatalf("cross-account substitution returned %v", err)
	}
}
