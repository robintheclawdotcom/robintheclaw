package main

import (
	"bytes"
	"context"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"errors"
	"io"
	"regexp"

	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const executionAliasPrefix = "alias/robinhood/execution/"

var kmsKeyARN = regexp.MustCompile(`^arn:(aws|aws-cn|aws-us-gov):kms:[a-z0-9-]+:[0-9]{12}:key/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type lambdaAPI interface {
	Invoke(context.Context, *awslambda.InvokeInput, ...func(*awslambda.Options)) (*awslambda.InvokeOutput, error)
}

type kmsKey struct {
	ID      string
	Address common.Address
}

type keyProvisioner struct {
	client      lambdaAPI
	functionARN string
}

type keyProvisionRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
}

type keyProvisionResponse struct {
	ExecutionAccountID string `json:"executionAccountId"`
	KeyARN             string `json:"keyArn"`
	Alias              string `json:"alias"`
	KeySpec            string `json:"keySpec"`
	KeyUsage           string `json:"keyUsage"`
	Origin             string `json:"origin"`
	PublicKey          []byte `json:"publicKey"`
}

type subjectPublicKeyInfo struct {
	Algorithm pkix.AlgorithmIdentifier
	PublicKey asn1.BitString
}

var (
	ecPublicKeyOID = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
	secp256k1OID   = asn1.ObjectIdentifier{1, 3, 132, 0, 10}
)

func (value *keyProvisioner) ensure(ctx context.Context, executionID string) (kmsKey, error) {
	payload, err := json.Marshal(keyProvisionRequest{ExecutionAccountID: executionID})
	if err != nil {
		return kmsKey{}, errors.New("encode Robinhood execution key request")
	}
	output, err := value.client.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName:   &value.functionARN,
		InvocationType: types.InvocationTypeRequestResponse,
		LogType:        types.LogTypeNone,
		Payload:        payload,
	})
	if err != nil || output == nil || output.StatusCode != 200 || output.FunctionError != nil || len(output.Payload) > 16<<10 {
		return kmsKey{}, errors.New("provision Robinhood execution key")
	}
	var binding keyProvisionResponse
	decoder := json.NewDecoder(bytes.NewReader(output.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&binding); err != nil {
		return kmsKey{}, errors.New("decode Robinhood execution key binding")
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return kmsKey{}, errors.New("decode Robinhood execution key binding")
	}
	if binding.ExecutionAccountID != executionID ||
		binding.Alias != executionAliasPrefix+executionID ||
		!kmsKeyARN.MatchString(binding.KeyARN) ||
		!sameAWSAuthority(value.functionARN, binding.KeyARN) ||
		binding.KeySpec != "ECC_SECG_P256K1" ||
		binding.KeyUsage != "SIGN_VERIFY" ||
		binding.Origin != "AWS_KMS" {
		return kmsKey{}, errors.New("Robinhood execution key binding mismatch")
	}
	address, err := publicKeyAddress(binding.PublicKey)
	if err != nil {
		return kmsKey{}, err
	}
	return kmsKey{ID: binding.KeyARN, Address: address}, nil
}

func sameAWSAuthority(functionARN, keyARN string) bool {
	function := bytes.Split([]byte(functionARN), []byte(":"))
	key := bytes.Split([]byte(keyARN), []byte(":"))
	return len(function) == 8 && len(key) == 6 &&
		bytes.Equal(function[1], key[1]) &&
		bytes.Equal(function[3], key[3]) &&
		bytes.Equal(function[4], key[4])
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func publicKeyAddress(encoded []byte) (common.Address, error) {
	var info subjectPublicKeyInfo
	if rest, err := asn1.Unmarshal(encoded, &info); err != nil || len(rest) != 0 {
		return common.Address{}, errors.New("decode Robinhood execution public key")
	}
	if !info.Algorithm.Algorithm.Equal(ecPublicKeyOID) || info.PublicKey.BitLength != len(info.PublicKey.Bytes)*8 {
		return common.Address{}, errors.New("Robinhood execution public key algorithm mismatch")
	}
	var curve asn1.ObjectIdentifier
	if rest, err := asn1.Unmarshal(info.Algorithm.Parameters.FullBytes, &curve); err != nil || len(rest) != 0 || !curve.Equal(secp256k1OID) {
		return common.Address{}, errors.New("Robinhood execution public key curve mismatch")
	}
	public, err := crypto.UnmarshalPubkey(info.PublicKey.Bytes)
	if err != nil {
		return common.Address{}, errors.New("parse Robinhood execution public key")
	}
	address := crypto.PubkeyToAddress(*public)
	if address == (common.Address{}) {
		return common.Address{}, errors.New("Robinhood execution public key produced zero address")
	}
	return address, nil
}
