package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestEnvelopeRejectsAADMismatchBeforeKMSDecrypt(t *testing.T) {
	kms := &fakeKMS{}
	vault := newEnvelope(kms, "alias/lighter")
	vault.rand = bytes.NewReader([]byte(strings.Repeat("n", 32)))
	record := credential{
		ExecutionAccountID: "11111111-1111-4111-8111-111111111111",
		AccountIndex:       42,
		APIKeyIndex:        4,
		Version:            1,
	}
	sealed, err := vault.seal(context.Background(), record, []byte("credential material"))
	if err != nil {
		t.Fatal(err)
	}
	record.EncryptedDataKey = sealed.EncryptedDataKey
	record.CipherNonce = sealed.Nonce
	record.Ciphertext = sealed.Ciphertext
	record.AADDigest = sealed.AADDigest
	record.KMSKeyID = sealed.KMSKeyID
	record.Version++
	if _, err := vault.open(context.Background(), record); err == nil || err.Error() != "credential binding mismatch" {
		t.Fatalf("expected binding mismatch, got %v", err)
	}
	if kms.decryptCalls != 0 {
		t.Fatalf("KMS decrypt called %d times", kms.decryptCalls)
	}
}
