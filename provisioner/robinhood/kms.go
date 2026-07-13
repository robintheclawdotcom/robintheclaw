package main

import (
	"context"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"

	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/smithy-go"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type kmsAPI interface {
	CreateKey(context.Context, *awskms.CreateKeyInput, ...func(*awskms.Options)) (*awskms.CreateKeyOutput, error)
	CreateAlias(context.Context, *awskms.CreateAliasInput, ...func(*awskms.Options)) (*awskms.CreateAliasOutput, error)
	DescribeKey(context.Context, *awskms.DescribeKeyInput, ...func(*awskms.Options)) (*awskms.DescribeKeyOutput, error)
	GetPublicKey(context.Context, *awskms.GetPublicKeyInput, ...func(*awskms.Options)) (*awskms.GetPublicKeyOutput, error)
}

type kmsKey struct {
	ID      string
	Address common.Address
}

type keyProvisioner struct {
	client      kmsAPI
	aliasPrefix string
}

type subjectPublicKeyInfo struct {
	Algorithm pkix.AlgorithmIdentifier
	PublicKey asn1.BitString
}

func (value *keyProvisioner) ensure(ctx context.Context, executionID string) (kmsKey, error) {
	alias := value.aliasPrefix + executionID
	described, err := value.client.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: &alias})
	if err != nil {
		var apiError smithy.APIError
		if !errors.As(err, &apiError) || apiError.ErrorCode() != "NotFoundException" {
			return kmsKey{}, errors.New("resolve Robinhood execution key")
		}
		created, err := value.client.CreateKey(ctx, &awskms.CreateKeyInput{
			Description: stringsPtr("Robinhood Chain execution key"),
			KeySpec:     types.KeySpecEccSecgP256k1,
			KeyUsage:    types.KeyUsageTypeSignVerify,
			Origin:      types.OriginTypeAwsKms,
			Tags: []types.Tag{
				{TagKey: stringsPtr("service"), TagValue: stringsPtr("robinhood-provisioner")},
				{TagKey: stringsPtr("executionAccountId"), TagValue: &executionID},
			},
		})
		if err != nil || created.KeyMetadata == nil || created.KeyMetadata.KeyId == nil {
			return kmsKey{}, errors.New("create Robinhood execution key")
		}
		keyID := *created.KeyMetadata.KeyId
		if _, err := value.client.CreateAlias(ctx, &awskms.CreateAliasInput{AliasName: &alias, TargetKeyId: &keyID}); err != nil {
			return kmsKey{}, errors.New("bind Robinhood execution key alias")
		}
		described, err = value.client.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: &alias})
		if err != nil {
			return kmsKey{}, errors.New("verify Robinhood execution key alias")
		}
	}
	if described.KeyMetadata == nil || described.KeyMetadata.KeyId == nil || described.KeyMetadata.Arn == nil ||
		described.KeyMetadata.KeySpec != types.KeySpecEccSecgP256k1 ||
		described.KeyMetadata.KeyUsage != types.KeyUsageTypeSignVerify ||
		described.KeyMetadata.Origin != types.OriginTypeAwsKms ||
		described.KeyMetadata.KeyManager != types.KeyManagerTypeCustomer ||
		described.KeyMetadata.KeyState != types.KeyStateEnabled {
		return kmsKey{}, errors.New("Robinhood execution key metadata mismatch")
	}
	keyID := *described.KeyMetadata.Arn
	output, err := value.client.GetPublicKey(ctx, &awskms.GetPublicKeyInput{KeyId: &keyID})
	if err != nil || output.KeySpec != types.KeySpecEccSecgP256k1 || output.KeyUsage != types.KeyUsageTypeSignVerify {
		return kmsKey{}, errors.New("read Robinhood execution public key")
	}
	var info subjectPublicKeyInfo
	if rest, err := asn1.Unmarshal(output.PublicKey, &info); err != nil || len(rest) != 0 {
		return kmsKey{}, errors.New("decode Robinhood execution public key")
	}
	public, err := crypto.UnmarshalPubkey(info.PublicKey.Bytes)
	if err != nil {
		return kmsKey{}, errors.New("parse Robinhood execution public key")
	}
	address := crypto.PubkeyToAddress(*public)
	if address == (common.Address{}) {
		return kmsKey{}, errors.New("Robinhood execution public key produced zero address")
	}
	return kmsKey{ID: keyID, Address: address}, nil
}

func stringsPtr(value string) *string { return &value }
