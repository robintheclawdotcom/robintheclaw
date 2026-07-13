package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

const (
	statusGenerating = "generating"
	statusPending    = "pending"
	statusVerifying  = "verifying"
	statusLinked     = "linked"
	statusSuperseded = "superseded"
	statusBlocked    = "blocked"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type binding struct {
	ExecutionAccountID string
	OwnerAddress       string
	AccountIndex       int64
	APIKeyIndex        uint8
	Status             string
	ActiveCredentialID string
}

type credential struct {
	ID                 string
	ExecutionAccountID string
	OwnerAddress       string
	AccountIndex       int64
	APIKeyIndex        uint8
	Version            int64
	PublicKey          string
	EncryptedDataKey   []byte
	CipherNonce        []byte
	Ciphertext         []byte
	AADDigest          []byte
	KMSKeyID           string
	ChangeNonce        int64
	ExpiresAtMS        int64
	TxType             uint8
	TxHash             string
	TxInfo             []byte
	MessageToSign      string
	L1Signature        string
	Status             string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type reservation struct {
	Credential credential
	Rotation   bool
	Existing   bool
}

type sealedSecret struct {
	EncryptedDataKey []byte
	Nonce            []byte
	Ciphertext       []byte
	AADDigest        []byte
	KMSKeyID         string
}

type association struct {
	TxType        uint8
	TxHash        string
	TxInfo        []byte
	MessageToSign string
}

type publicLink struct {
	LinkID             string `json:"linkId"`
	ExecutionAccountID string `json:"executionAccountId"`
	OwnerAddress       string `json:"ownerAddress"`
	AccountIndex       int64  `json:"accountIndex"`
	APIKeyIndex        uint8  `json:"apiKeyIndex"`
	CredentialVersion  int64  `json:"credentialVersion"`
	PublicKey          string `json:"publicKey,omitempty"`
	Status             string `json:"status"`
	MessageToSign      string `json:"messageToSign,omitempty"`
	TransactionHash    string `json:"transactionHash,omitempty"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
}

func (value credential) public() publicLink {
	result := publicLink{
		LinkID:             value.ID,
		ExecutionAccountID: value.ExecutionAccountID,
		OwnerAddress:       value.OwnerAddress,
		AccountIndex:       value.AccountIndex,
		APIKeyIndex:        value.APIKeyIndex,
		CredentialVersion:  value.Version,
		PublicKey:          value.PublicKey,
		Status:             value.Status,
		TransactionHash:    value.TxHash,
		CreatedAt:          value.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:          value.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if value.Status == statusPending || value.Status == statusGenerating {
		result.MessageToSign = value.MessageToSign
	}
	return result
}

func validateExecutionAccountID(value string) error {
	if !uuidPattern.MatchString(strings.ToLower(value)) {
		return errors.New("executionAccountId must be a UUID")
	}
	return nil
}

func normalizeAddress(value string) (string, error) {
	if !common.IsHexAddress(value) || common.HexToAddress(value) == (common.Address{}) {
		return "", errors.New("ownerAddress must be a non-zero EVM address")
	}
	return strings.ToLower(common.HexToAddress(value).Hex()), nil
}

func normalizePublicKey(value string) string {
	return strings.ToLower(strings.TrimPrefix(value, "0x"))
}

func validateAccount(accountIndex int64, apiKeyIndex uint8) error {
	if accountIndex <= 0 {
		return errors.New("accountIndex must be positive")
	}
	if apiKeyIndex < 4 || apiKeyIndex > 254 {
		return errors.New("apiKeyIndex must be between 4 and 254")
	}
	return nil
}

func aadFor(value credential) []byte {
	return []byte(fmt.Sprintf(
		"lighter-credential/v1\n%s\n%d\n%d\n%d",
		strings.ToLower(value.ExecutionAccountID),
		value.AccountIndex,
		value.APIKeyIndex,
		value.Version,
	))
}

func encryptionContext(value credential) map[string]string {
	return map[string]string{
		"service":            "lighter-provisioner",
		"executionAccountId": strings.ToLower(value.ExecutionAccountID),
		"accountIndex":       fmt.Sprintf("%d", value.AccountIndex),
		"apiKeyIndex":        fmt.Sprintf("%d", value.APIKeyIndex),
		"credentialVersion":  fmt.Sprintf("%d", value.Version),
	}
}
