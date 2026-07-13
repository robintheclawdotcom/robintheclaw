package main

import (
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestSDKAssociationBoundaryNeedsOnlyWalletSignature(t *testing.T) {
	lighter := newLiveLighterClient("https://mainnet.zklighter.elliot.ai", 300)
	secret, publicKey, err := lighter.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	unsigned, err := lighter.BuildAssociation(secret, publicKey, 42, 3, 7, 2_000_000_600_000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(unsigned.MessageToSign, "Register Lighter Account\n") {
		t.Fatalf("unexpected association message: %q", unsigned.MessageToSign)
	}
	wallet, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signature, err := crypto.Sign(accounts.TextHash([]byte(unsigned.MessageToSign)), wallet)
	if err != nil {
		t.Fatal(err)
	}
	finalized, recovered, err := lighter.FinalizeAssociation(
		secret,
		publicKey,
		42,
		3,
		7,
		2_000_000_600_000,
		hexutil.Encode(signature),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(recovered, crypto.PubkeyToAddress(wallet.PublicKey).Hex()) {
		t.Fatalf("recovered owner = %s", recovered)
	}
	if finalized.TxHash != unsigned.TxHash || finalized.MessageToSign != unsigned.MessageToSign {
		t.Fatal("wallet signature changed the association identity")
	}
}
