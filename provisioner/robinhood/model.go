package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

const (
	statusAwaitingDeployment = "awaiting_deployment"
	statusConfirming         = "confirming"
	statusActive             = "active"
	statusRotationPending    = "rotation_pending"
	statusBlocked            = "blocked"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type graph struct {
	RiskManager string `json:"riskManager"`
	SpotAdapter string `json:"spotAdapter"`
	Vault       string `json:"vault"`
}

type binding struct {
	ExecutionAccountID string
	OwnerAddress       string
	KMSKeyID           string
	SignerAddress      string
	KeyVersion         int64
	FactoryAddress     string
	RegistryAddress    string
	PolicyDigest       string
	FactoryCodeHash    string
	RegistryCodeHash   string
	VaultCodeHash      string
	RiskCodeHash       string
	AdapterCodeHash    string
	Graph              graph
	DeploymentTxHash   string
	DeploymentBlock    uint64
	Status             string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type prepareRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
	OwnerAddress       string `json:"ownerAddress"`
}

type confirmRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
	DeploymentTxHash   string `json:"deploymentTransactionHash"`
}

type statusRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
}

type resolveRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
}

type unsignedAction struct {
	Kind    string `json:"kind"`
	ChainID string `json:"chainId"`
	To      string `json:"to"`
	Value   string `json:"value"`
	Data    string `json:"data"`
}

type publicBinding struct {
	ExecutionAccountID string           `json:"executionAccountId"`
	OwnerAddress       string           `json:"ownerAddress"`
	SignerAddress      string           `json:"signerAddress"`
	KeyVersion         int64            `json:"keyVersion"`
	FactoryAddress     string           `json:"factoryAddress"`
	RegistryAddress    string           `json:"registryAddress"`
	PolicyDigest       string           `json:"policyDigest"`
	Graph              graph            `json:"graph"`
	Status             string           `json:"status"`
	DeploymentTxHash   string           `json:"deploymentTransactionHash,omitempty"`
	DeploymentBlock    uint64           `json:"deploymentBlock,omitempty"`
	Actions            []unsignedAction `json:"actions,omitempty"`
	UpdatedAt          string           `json:"updatedAt"`
}

type resolvedBinding struct {
	ExecutionAccountID  string `json:"executionAccountId"`
	OwnerAddress        string `json:"ownerAddress"`
	KMSKeyID            string `json:"kmsKeyId"`
	SignerAddress       string `json:"signerAddress"`
	KeyVersion          int64  `json:"keyVersion"`
	FactoryAddress      string `json:"factoryAddress"`
	FactoryCodeHash     string `json:"factoryCodeHash"`
	RegistryAddress     string `json:"registryAddress"`
	RegistryCodeHash    string `json:"registryCodeHash"`
	PolicyDigest        string `json:"policyDigest"`
	VaultAddress        string `json:"vaultAddress"`
	VaultCodeHash       string `json:"vaultCodeHash"`
	RiskManagerAddress  string `json:"riskManagerAddress"`
	RiskManagerCodeHash string `json:"riskManagerCodeHash"`
	SpotAdapterAddress  string `json:"spotAdapterAddress"`
	SpotAdapterCodeHash string `json:"spotAdapterCodeHash"`
	BindingSHA256       string `json:"bindingSha256"`
}

func (value binding) public(actions []unsignedAction) publicBinding {
	return publicBinding{
		ExecutionAccountID: value.ExecutionAccountID,
		OwnerAddress:       value.OwnerAddress,
		SignerAddress:      value.SignerAddress,
		KeyVersion:         value.KeyVersion,
		FactoryAddress:     value.FactoryAddress,
		RegistryAddress:    value.RegistryAddress,
		PolicyDigest:       value.PolicyDigest,
		Graph:              value.Graph,
		Status:             value.Status,
		DeploymentTxHash:   value.DeploymentTxHash,
		DeploymentBlock:    value.DeploymentBlock,
		Actions:            actions,
		UpdatedAt:          value.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (value binding) resolved() resolvedBinding {
	result := resolvedBinding{
		ExecutionAccountID:  value.ExecutionAccountID,
		OwnerAddress:        value.OwnerAddress,
		KMSKeyID:            value.KMSKeyID,
		SignerAddress:       value.SignerAddress,
		KeyVersion:          value.KeyVersion,
		FactoryAddress:      value.FactoryAddress,
		FactoryCodeHash:     value.FactoryCodeHash,
		RegistryAddress:     value.RegistryAddress,
		RegistryCodeHash:    value.RegistryCodeHash,
		PolicyDigest:        value.PolicyDigest,
		VaultAddress:        value.Graph.Vault,
		VaultCodeHash:       value.VaultCodeHash,
		RiskManagerAddress:  value.Graph.RiskManager,
		RiskManagerCodeHash: value.RiskCodeHash,
		SpotAdapterAddress:  value.Graph.SpotAdapter,
		SpotAdapterCodeHash: value.AdapterCodeHash,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	result.BindingSHA256 = hex.EncodeToString(digest[:])
	return result
}

func normalizeExecutionAccountID(value string) (string, error) {
	value = strings.ToLower(value)
	if !uuidPattern.MatchString(value) {
		return "", errors.New("executionAccountId must be a UUIDv4")
	}
	return value, nil
}

func normalizeAddress(value string) (string, error) {
	if !common.IsHexAddress(value) || common.HexToAddress(value) == (common.Address{}) {
		return "", errors.New("address must be non-zero")
	}
	return strings.ToLower(common.HexToAddress(value).Hex()), nil
}

func normalizeHash(value string) (string, error) {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") {
		return "", errors.New("hash must be bytes32")
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil || len(decoded) != 32 || common.BytesToHash(decoded) == (common.Hash{}) {
		return "", errors.New("hash must be non-zero bytes32")
	}
	return strings.ToLower(value), nil
}
