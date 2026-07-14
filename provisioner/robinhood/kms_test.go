package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509/pkix"
	"encoding/asn1"
	"testing"

	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type fakeKMS struct {
	private     *ecdsa.PrivateKey
	executionID string
}

func (value fakeKMS) DescribeKey(context.Context, *awskms.DescribeKeyInput, ...func(*awskms.Options)) (*awskms.DescribeKeyOutput, error) {
	id := "key-id"
	arn := "arn:aws:kms:region:account:key/key-id"
	return &awskms.DescribeKeyOutput{KeyMetadata: &types.KeyMetadata{
		KeyId:      &id,
		Arn:        &arn,
		KeySpec:    types.KeySpecEccSecgP256k1,
		KeyUsage:   types.KeyUsageTypeSignVerify,
		Origin:     types.OriginTypeAwsKms,
		KeyManager: types.KeyManagerTypeCustomer,
		KeyState:   types.KeyStateEnabled,
	}}, nil
}

func (fakeKMS) CreateKey(context.Context, *awskms.CreateKeyInput, ...func(*awskms.Options)) (*awskms.CreateKeyOutput, error) {
	panic("unexpected key creation")
}

func (fakeKMS) CreateAlias(context.Context, *awskms.CreateAliasInput, ...func(*awskms.Options)) (*awskms.CreateAliasOutput, error) {
	panic("unexpected alias creation")
}

func (value fakeKMS) GetPublicKey(context.Context, *awskms.GetPublicKeyInput, ...func(*awskms.Options)) (*awskms.GetPublicKeyOutput, error) {
	algorithm := pkix.AlgorithmIdentifier{
		Algorithm:  asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1},
		Parameters: asn1.RawValue{FullBytes: mustASN1(asn1.ObjectIdentifier{1, 3, 132, 0, 10})},
	}
	encoded := mustASN1(subjectPublicKeyInfo{
		Algorithm: algorithm,
		PublicKey: asn1.BitString{Bytes: crypto.FromECDSAPub(&value.private.PublicKey), BitLength: 520},
	})
	return &awskms.GetPublicKeyOutput{KeySpec: types.KeySpecEccSecgP256k1, KeyUsage: types.KeyUsageTypeSignVerify, PublicKey: encoded}, nil
}

func (value fakeKMS) ListResourceTags(context.Context, *awskms.ListResourceTagsInput, ...func(*awskms.Options)) (*awskms.ListResourceTagsOutput, error) {
	return &awskms.ListResourceTagsOutput{Tags: []types.Tag{
		{TagKey: stringsPtr("service"), TagValue: stringsPtr("robinhood-provisioner")},
		{TagKey: stringsPtr("executionAccountId"), TagValue: stringsPtr(value.executionID)},
	}}, nil
}

func TestExistingKMSKeyMustBeNonExportableSecp256k1(t *testing.T) {
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	provisioner := keyProvisioner{client: fakeKMS{private: private, executionID: testExecutionID}, aliasPrefix: "alias/robinhood/execution/"}
	key, err := provisioner.ensure(context.Background(), testExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if key.ID != "arn:aws:kms:region:account:key/key-id" || key.Address != crypto.PubkeyToAddress(private.PublicKey) {
		t.Fatalf("unexpected execution key: %#v", key)
	}
}

func TestExistingKMSKeyMustBelongToExecutionAccount(t *testing.T) {
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	provisioner := keyProvisioner{
		client:      fakeKMS{private: private, executionID: "22222222-2222-4222-8222-222222222222"},
		aliasPrefix: "alias/robinhood/execution/",
	}
	if _, err := provisioner.ensure(context.Background(), testExecutionID); err == nil {
		t.Fatal("expected execution account tag mismatch")
	}
}

func mustASN1(value any) []byte {
	encoded, err := asn1.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
