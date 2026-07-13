package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"testing"

	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type signingKMS struct {
	fakeKMS
	signature []byte
	signErr   error
}

func (kms signingKMS) Sign(_ context.Context, _ *awskms.SignInput, _ ...func(*awskms.Options)) (*awskms.SignOutput, error) {
	if kms.signErr != nil {
		return nil, kms.signErr
	}
	return &awskms.SignOutput{Signature: kms.signature}, nil
}

type fakeKMS struct {
	private *ecdsa.PrivateKey
}

func (fake fakeKMS) GetPublicKey(_ context.Context, _ *awskms.GetPublicKeyInput, _ ...func(*awskms.Options)) (*awskms.GetPublicKeyOutput, error) {
	algorithm := pkix.AlgorithmIdentifier{
		Algorithm:  asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1},
		Parameters: asn1.RawValue{FullBytes: mustASN1(asn1.ObjectIdentifier{1, 3, 132, 0, 10})},
	}
	encoded := mustASN1(subjectPublicKeyInfo{
		Algorithm: algorithm,
		PublicKey: asn1.BitString{Bytes: crypto.FromECDSAPub(&fake.private.PublicKey), BitLength: 520},
	})
	return &awskms.GetPublicKeyOutput{
		KeySpec:   types.KeySpecEccSecgP256k1,
		KeyUsage:  types.KeyUsageTypeSignVerify,
		PublicKey: encoded,
	}, nil
}

func (fake fakeKMS) Sign(_ context.Context, input *awskms.SignInput, _ ...func(*awskms.Options)) (*awskms.SignOutput, error) {
	signature, err := crypto.Sign(input.Message, fake.private)
	if err != nil {
		return nil, err
	}
	encoded := mustASN1(ecdsaSignature{
		R: new(big.Int).SetBytes(signature[:32]),
		S: new(big.Int).SetBytes(signature[32:64]),
	})
	return &awskms.SignOutput{Signature: encoded}, nil
}

func TestKMSSignatureRecoversExpectedAddress(t *testing.T) {
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := newKMSSigner(context.Background(), fakeKMS{private: private}, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	digest := crypto.Keccak256([]byte("intent"))
	signature, err := signer.SignDigest(context.Background(), digest)
	if err != nil {
		t.Fatal(err)
	}
	public, err := crypto.SigToPub(digest, signature)
	if err != nil {
		t.Fatal(err)
	}
	if crypto.PubkeyToAddress(*public) != signer.Address() {
		t.Fatal("signature recovered another address")
	}
}

func TestKMSNormalizesHighS(t *testing.T) {
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	digest := crypto.Keccak256([]byte("high-s"))
	signature, err := crypto.Sign(digest, private)
	if err != nil {
		t.Fatal(err)
	}
	highS := new(big.Int).Sub(crypto.S256().Params().N, new(big.Int).SetBytes(signature[32:64]))
	encoded := mustASN1(ecdsaSignature{
		R: new(big.Int).SetBytes(signature[:32]),
		S: highS,
	})
	signer, err := newKMSSigner(
		context.Background(),
		signingKMS{fakeKMS: fakeKMS{private: private}, signature: encoded},
		"test-key",
	)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := signer.SignDigest(context.Background(), digest)
	if err != nil {
		t.Fatal(err)
	}
	if new(big.Int).SetBytes(normalized[32:64]).Cmp(new(big.Int).Rsh(new(big.Int).Set(crypto.S256().Params().N), 1)) > 0 {
		t.Fatal("signature was not normalized to low-S")
	}
}

func TestKMSFindsRecoveryIDOne(t *testing.T) {
	digest := crypto.Keccak256([]byte("recovery-one"))
	for attempt := 0; attempt < 100; attempt++ {
		private, err := crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		reference, err := crypto.Sign(digest, private)
		if err != nil {
			t.Fatal(err)
		}
		if reference[64] != 1 {
			continue
		}
		signer, err := newKMSSigner(context.Background(), fakeKMS{private: private}, "test-key")
		if err != nil {
			t.Fatal(err)
		}
		signature, err := signer.SignDigest(context.Background(), digest)
		if err != nil {
			t.Fatal(err)
		}
		if signature[64] != 1 {
			t.Fatalf("unexpected recovery ID: %d", signature[64])
		}
		return
	}
	t.Fatal("could not generate recovery ID 1 test vector")
}

func TestKMSRejectsMalformedAndRetargetedSignatures(t *testing.T) {
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := newKMSSigner(
		context.Background(),
		signingKMS{fakeKMS: fakeKMS{private: private}, signature: []byte{1, 2, 3}},
		"test-key",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.SignDigest(context.Background(), make([]byte, 32)); err == nil {
		t.Fatal("malformed DER signature was accepted")
	}
	other, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	digest := crypto.Keccak256([]byte("retargeted"))
	otherSignature, err := crypto.Sign(digest, other)
	if err != nil {
		t.Fatal(err)
	}
	retargeted := mustASN1(ecdsaSignature{
		R: new(big.Int).SetBytes(otherSignature[:32]),
		S: new(big.Int).SetBytes(otherSignature[32:64]),
	})
	signer.client = signingKMS{fakeKMS: fakeKMS{private: private}, signature: retargeted}
	if _, err := signer.SignDigest(context.Background(), digest); err == nil {
		t.Fatal("signature from a retargeted key was accepted")
	}
	signer.client = signingKMS{fakeKMS: fakeKMS{private: private}, signErr: errors.New("timeout")}
	if _, err := signer.SignDigest(context.Background(), digest); err == nil {
		t.Fatal("KMS failure was accepted")
	}
}

func mustASN1(value any) []byte {
	encoded, err := asn1.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
