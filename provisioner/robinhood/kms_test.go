package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"strings"
	"testing"

	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const testControlPlaneARN = "arn:aws:lambda:us-east-1:123456789012:function:robinhood-key-control-plane:17"

type fakeLambda struct {
	private      *ecdsa.PrivateKey
	executionID  string
	keyARN       string
	unknownField bool
	functionErr  bool
	invoked      *awslambda.InvokeInput
}

func (value *fakeLambda) Invoke(_ context.Context, input *awslambda.InvokeInput, _ ...func(*awslambda.Options)) (*awslambda.InvokeOutput, error) {
	value.invoked = input
	algorithm := pkix.AlgorithmIdentifier{
		Algorithm:  ecPublicKeyOID,
		Parameters: asn1.RawValue{FullBytes: mustASN1(secp256k1OID)},
	}
	encoded := mustASN1(subjectPublicKeyInfo{
		Algorithm: algorithm,
		PublicKey: asn1.BitString{Bytes: crypto.FromECDSAPub(&value.private.PublicKey), BitLength: 520},
	})
	keyARN := value.keyARN
	if keyARN == "" {
		keyARN = "arn:aws:kms:us-east-1:123456789012:key/11111111-1111-4111-8111-111111111111"
	}
	response := map[string]any{
		"executionAccountId": value.executionID,
		"keyArn":             keyARN,
		"alias":              executionAliasPrefix + value.executionID,
		"keySpec":            "ECC_SECG_P256K1",
		"keyUsage":           "SIGN_VERIFY",
		"origin":             "AWS_KMS",
		"publicKey":          encoded,
	}
	if value.unknownField {
		response["policy"] = "caller-controlled"
	}
	payload, err := json.Marshal(response)
	if err != nil {
		panic(err)
	}
	output := &awslambda.InvokeOutput{Payload: payload, StatusCode: 200}
	if value.functionErr {
		failure := "Unhandled"
		output.FunctionError = &failure
	}
	return output, nil
}

func TestKeyControlPlaneReturnsNonExportableSecp256k1Binding(t *testing.T) {
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeLambda{private: private, executionID: testExecutionID}
	provisioner := keyProvisioner{client: client, functionARN: testControlPlaneARN}
	key, err := provisioner.ensure(context.Background(), testExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if key.ID != "arn:aws:kms:us-east-1:123456789012:key/11111111-1111-4111-8111-111111111111" ||
		key.Address != crypto.PubkeyToAddress(private.PublicKey) {
		t.Fatalf("unexpected execution key: %#v", key)
	}
	if client.invoked == nil || client.invoked.FunctionName == nil || *client.invoked.FunctionName != testControlPlaneARN {
		t.Fatal("wrong key control-plane function")
	}
	if client.invoked.InvocationType != types.InvocationTypeRequestResponse || client.invoked.LogType != types.LogTypeNone {
		t.Fatal("unsafe key control-plane invocation mode")
	}
	if string(client.invoked.Payload) != `{"executionAccountId":"`+testExecutionID+`"}` {
		t.Fatalf("unexpected key control-plane request: %s", client.invoked.Payload)
	}
}

func TestKeyControlPlaneBindingMustBelongToExecutionAccount(t *testing.T) {
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	provisioner := keyProvisioner{
		client:      &fakeLambda{private: private, executionID: "22222222-2222-4222-8222-222222222222"},
		functionARN: testControlPlaneARN,
	}
	if _, err := provisioner.ensure(context.Background(), testExecutionID); err == nil {
		t.Fatal("expected execution account binding mismatch")
	}
}

func TestKeyControlPlaneBindingMustShareAWSAuthority(t *testing.T) {
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	provisioner := keyProvisioner{
		client: &fakeLambda{
			private:     private,
			executionID: testExecutionID,
			keyARN:      "arn:aws:kms:us-east-1:999999999999:key/11111111-1111-4111-8111-111111111111",
		},
		functionARN: testControlPlaneARN,
	}
	if _, err := provisioner.ensure(context.Background(), testExecutionID); err == nil {
		t.Fatal("expected AWS authority mismatch")
	}
}

func TestKeyControlPlaneRejectsExtendedResponse(t *testing.T) {
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	provisioner := keyProvisioner{
		client:      &fakeLambda{private: private, executionID: testExecutionID, unknownField: true},
		functionARN: testControlPlaneARN,
	}
	if _, err := provisioner.ensure(context.Background(), testExecutionID); err == nil {
		t.Fatal("expected unknown response field rejection")
	}
}

func TestKeyControlPlaneRejectsFunctionError(t *testing.T) {
	private, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	provisioner := keyProvisioner{
		client:      &fakeLambda{private: private, executionID: testExecutionID, functionErr: true},
		functionARN: testControlPlaneARN,
	}
	_, err = provisioner.ensure(context.Background(), testExecutionID)
	if err == nil || !strings.Contains(err.Error(), "provision Robinhood execution key") {
		t.Fatal("expected function error rejection")
	}
}

func mustASN1(value any) []byte {
	encoded, err := asn1.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
