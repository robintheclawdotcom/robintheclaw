package main

import (
	"context"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"

	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type kmsAPI interface {
	GetPublicKey(context.Context, *awskms.GetPublicKeyInput, ...func(*awskms.Options)) (*awskms.GetPublicKeyOutput, error)
	Sign(context.Context, *awskms.SignInput, ...func(*awskms.Options)) (*awskms.SignOutput, error)
}

type KMSSigner struct {
	client  kmsAPI
	keyID   string
	address common.Address
}

type subjectPublicKeyInfo struct {
	Algorithm pkix.AlgorithmIdentifier
	PublicKey asn1.BitString
}

type ecdsaSignature struct {
	R *big.Int
	S *big.Int
}

func newKMSSigner(ctx context.Context, client kmsAPI, keyID string) (*KMSSigner, error) {
	output, err := client.GetPublicKey(ctx, &awskms.GetPublicKeyInput{KeyId: &keyID})
	if err != nil {
		return nil, errors.New("read KMS public key")
	}
	if output.KeySpec != types.KeySpecEccSecgP256k1 || output.KeyUsage != types.KeyUsageTypeSignVerify {
		return nil, errors.New("KMS key must be ECC_SECG_P256K1 with SIGN_VERIFY usage")
	}
	var info subjectPublicKeyInfo
	if rest, err := asn1.Unmarshal(output.PublicKey, &info); err != nil || len(rest) != 0 {
		return nil, errors.New("decode KMS public key")
	}
	public, err := crypto.UnmarshalPubkey(info.PublicKey.Bytes)
	if err != nil {
		return nil, errors.New("parse KMS secp256k1 public key")
	}
	return &KMSSigner{
		client:  client,
		keyID:   keyID,
		address: crypto.PubkeyToAddress(*public),
	}, nil
}

func (signer *KMSSigner) Address() common.Address {
	return signer.address
}

func (signer *KMSSigner) SignDigest(ctx context.Context, digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, errors.New("KMS signer requires a 32-byte digest")
	}
	output, err := signer.client.Sign(ctx, &awskms.SignInput{
		KeyId:            &signer.keyID,
		Message:          digest,
		MessageType:      types.MessageTypeDigest,
		SigningAlgorithm: types.SigningAlgorithmSpecEcdsaSha256,
	})
	if err != nil {
		return nil, errors.New("KMS signing failed")
	}
	var decoded ecdsaSignature
	if rest, err := asn1.Unmarshal(output.Signature, &decoded); err != nil || len(rest) != 0 {
		return nil, errors.New("decode KMS signature")
	}
	curveN := crypto.S256().Params().N
	if decoded.R == nil || decoded.S == nil || decoded.R.Sign() <= 0 || decoded.S.Sign() <= 0 || decoded.R.Cmp(curveN) >= 0 || decoded.S.Cmp(curveN) >= 0 {
		return nil, errors.New("KMS returned an invalid signature")
	}
	halfN := new(big.Int).Rsh(new(big.Int).Set(curveN), 1)
	if decoded.S.Cmp(halfN) > 0 {
		decoded.S.Sub(curveN, decoded.S)
	}
	signature := make([]byte, crypto.SignatureLength)
	decoded.R.FillBytes(signature[:32])
	decoded.S.FillBytes(signature[32:64])
	for recoveryID := byte(0); recoveryID < 2; recoveryID++ {
		signature[64] = recoveryID
		recovered, err := crypto.SigToPub(digest, signature)
		if err == nil && crypto.PubkeyToAddress(*recovered) == signer.address {
			return signature, nil
		}
	}
	return nil, errors.New("KMS signature does not recover to configured key")
}
