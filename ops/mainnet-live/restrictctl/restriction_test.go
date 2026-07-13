package restrictctl

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestSignAndVerifyBindsRequest(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	request := validAccountRequest()
	signed, err := Sign(request, privateKey, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := signed.Verify(); err != nil {
		t.Fatal(err)
	}
	again, err := Sign(request, privateKey, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if signed.SHA256 != again.SHA256 || !bytes.Equal(signed.Signature, again.Signature) {
		t.Fatal("same request and key must produce the same digest and signature")
	}
	tampered := signed
	tampered.Request.Reason = "different operator restriction reason"
	if err := tampered.Verify(); err == nil {
		t.Fatal("tampered request verified")
	}
	tampered = signed
	tampered.Signature = append([]byte(nil), signed.Signature...)
	tampered.Signature[0] ^= 0xff
	if err := tampered.Verify(); err == nil {
		t.Fatal("tampered signature verified")
	}
}

func TestValidateScopeAndTarget(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{"active target", func(value *Request) { value.TargetMode = ModeActive }},
		{"reverse transition", func(value *Request) { value.FromMode = ModeHalted }},
		{"negative version", func(value *Request) { value.ExpectedVersion = -1 }},
		{"bad evidence", func(value *Request) { value.EvidenceSHA256 = strings.Repeat("A", 64) }},
		{"untrimmed reason", func(value *Request) { value.Reason = " restriction pending evidence" }},
		{"missing account", func(value *Request) { value.ExecutionAccountID = "" }},
		{"missing strategy", func(value *Request) { value.StrategyVersion = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validAccountRequest()
			test.mutate(&request)
			if err := Validate(request); err == nil {
				t.Fatal("invalid request was accepted")
			}
		})
	}
	global := validAccountRequest()
	global.Scope = ScopeGlobal
	global.StrategyVersion = ""
	global.ExecutionAccountID = ""
	if err := Validate(global); err != nil {
		t.Fatal(err)
	}
	strategy := validAccountRequest()
	strategy.Scope = ScopeStrategy
	strategy.ExecutionAccountID = ""
	if err := Validate(strategy); err != nil {
		t.Fatal(err)
	}
}

func TestAllowedTransition(t *testing.T) {
	allowed := [][2]Mode{
		{ModeActive, ModeReduceOnly},
		{ModeActive, ModeHalted},
		{ModeReduceOnly, ModeHalted},
	}
	for _, transition := range allowed {
		if !AllowedTransition(transition[0], transition[1]) {
			t.Fatalf("expected %s -> %s to be allowed", transition[0], transition[1])
		}
	}
	blocked := [][2]Mode{
		{ModeActive, ModeActive},
		{ModeReduceOnly, ModeReduceOnly},
		{ModeReduceOnly, ModeActive},
		{ModeHalted, ModeReduceOnly},
		{ModeHalted, ModeActive},
	}
	for _, transition := range blocked {
		if AllowedTransition(transition[0], transition[1]) {
			t.Fatalf("expected %s -> %s to be blocked", transition[0], transition[1])
		}
	}
}

func TestLoadKeyPairRequiresOwnerOnlyFileAndMatchingKey(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	privatePath := filepath.Join(directory, "operator-private.pem")
	publicPath := filepath.Join(directory, "operator-public.pem")
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadKeyPair(privatePath, publicPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(privatePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadKeyPair(privatePath, publicPath); err == nil {
		t.Fatal("group-readable private key was accepted")
	}
}

func validAccountRequest() Request {
	return Request{
		RequestID:          "ops-account-0001",
		Scope:              ScopeAccount,
		StrategyVersion:    "basis-aapl-v1",
		ExecutionAccountID: "account-00000001",
		ExpectedVersion:    0,
		FromMode:           ModeActive,
		TargetMode:         ModeReduceOnly,
		Reason:             "operator restriction pending reconciliation",
		EvidenceSHA256:     testDigest,
		OperatorID:         "primary-operator",
	}
}
