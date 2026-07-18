package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type config struct {
	Enabled              bool
	RunMigrations        bool
	ListenAddress        string
	DatabaseURL          string
	APIHMACKey           []byte
	CallerID             string
	SignerHMACKey        []byte
	SignerCallerID       string
	PrimaryRPCURL        string
	SecondaryRPCURL      string
	ChainID              *big.Int
	FactoryAddress       common.Address
	RegistryAddress      common.Address
	PolicyDigest         common.Hash
	FactoryCodeHash      common.Hash
	RegistryCodeHash     common.Hash
	VaultCodeHash        common.Hash
	RiskManagerCodeHash  common.Hash
	SpotAdapterCodeHash  common.Hash
	FinalityBlocks       uint64
	RequestTimeout       time.Duration
	KMSControlPlaneARN   string
	MaxRequestsPerMinute uint16
}

func loadConfig() (config, error) {
	value := config{
		Enabled:              strings.EqualFold(os.Getenv("ROBINHOOD_PROVISIONER_ENABLED"), "true"),
		RunMigrations:        true,
		ListenAddress:        envOr("LISTEN_ADDRESS", "127.0.0.1:8080"),
		ChainID:              big.NewInt(4663),
		FinalityBlocks:       64,
		RequestTimeout:       20 * time.Second,
		MaxRequestsPerMinute: 60,
	}
	if !value.Enabled {
		return value, nil
	}
	if raw := os.Getenv("ROBINHOOD_PROVISIONER_RUN_MIGRATIONS"); raw != "" {
		switch raw {
		case "true":
			value.RunMigrations = true
		case "false":
			value.RunMigrations = false
		default:
			return config{}, errors.New("ROBINHOOD_PROVISIONER_RUN_MIGRATIONS must be true or false")
		}
	}
	var err error
	value.APIHMACKey, err = decodeKey("ROBINHOOD_PROVISIONER_HMAC_KEY")
	if err != nil {
		return config{}, err
	}
	value.SignerHMACKey, err = decodeKey("ROBINHOOD_SIGNER_BRIDGE_HMAC_KEY")
	if err != nil {
		return config{}, err
	}
	if bytes.Equal(value.APIHMACKey, value.SignerHMACKey) {
		return config{}, errors.New("product and signer bridge HMAC keys must be distinct")
	}
	value.CallerID = os.Getenv("ROBINHOOD_PROVISIONER_CALLER_ID")
	value.SignerCallerID = os.Getenv("ROBINHOOD_SIGNER_BRIDGE_CALLER_ID")
	if !validCallerID(value.CallerID) || !validCallerID(value.SignerCallerID) || value.CallerID == value.SignerCallerID {
		return config{}, errors.New("provisioner callers must be distinct lowercase service identifiers")
	}
	value.DatabaseURL = os.Getenv("DATABASE_URL")
	if value.DatabaseURL == "" {
		return config{}, errors.New("DATABASE_URL is required")
	}
	value.PrimaryRPCURL = os.Getenv("ROBINHOOD_RPC_URL")
	value.SecondaryRPCURL = os.Getenv("ROBINHOOD_RECONCILIATION_RPC_URL")
	if err := validateRPCs(value.PrimaryRPCURL, value.SecondaryRPCURL); err != nil {
		return config{}, err
	}
	if raw := os.Getenv("ROBINHOOD_CHAIN_ID"); raw != "" {
		value.ChainID, _ = new(big.Int).SetString(raw, 10)
		if value.ChainID == nil || value.ChainID.Sign() <= 0 {
			return config{}, errors.New("ROBINHOOD_CHAIN_ID must be positive")
		}
	}
	if value.FactoryAddress, err = requiredAddress("ROBINHOOD_USER_VAULT_FACTORY"); err != nil {
		return config{}, err
	}
	if value.RegistryAddress, err = requiredAddress("ROBINHOOD_EXECUTION_REGISTRY"); err != nil {
		return config{}, err
	}
	if value.FactoryAddress == value.RegistryAddress {
		return config{}, errors.New("factory and registry addresses must be distinct")
	}
	for name, target := range map[string]*common.Hash{
		"ROBINHOOD_POLICY_DIGEST":          &value.PolicyDigest,
		"ROBINHOOD_FACTORY_CODE_HASH":      &value.FactoryCodeHash,
		"ROBINHOOD_REGISTRY_CODE_HASH":     &value.RegistryCodeHash,
		"ROBINHOOD_USER_VAULT_CODE_HASH":   &value.VaultCodeHash,
		"ROBINHOOD_RISK_MANAGER_CODE_HASH": &value.RiskManagerCodeHash,
		"ROBINHOOD_SPOT_ADAPTER_CODE_HASH": &value.SpotAdapterCodeHash,
	} {
		*target, err = requiredHash(name)
		if err != nil {
			return config{}, err
		}
	}
	if raw := os.Getenv("ROBINHOOD_FINALITY_BLOCKS"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || parsed == 0 || parsed > 100_000 {
			return config{}, errors.New("ROBINHOOD_FINALITY_BLOCKS must be between 1 and 100000")
		}
		value.FinalityBlocks = parsed
	}
	value.KMSControlPlaneARN = os.Getenv("ROBINHOOD_KMS_PROVISION_FUNCTION_ARN")
	if !validLambdaVersionARN(value.KMSControlPlaneARN) {
		return config{}, errors.New("ROBINHOOD_KMS_PROVISION_FUNCTION_ARN must be a published AWS Lambda version ARN")
	}
	if raw := os.Getenv("PROVISIONER_MAX_REQUESTS_PER_MINUTE"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 16)
		if err != nil || parsed == 0 || parsed > 600 {
			return config{}, errors.New("PROVISIONER_MAX_REQUESTS_PER_MINUTE must be between 1 and 600")
		}
		value.MaxRequestsPerMinute = uint16(parsed)
	}
	return value, nil
}

func validLambdaVersionARN(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 8 || parts[0] != "arn" || parts[2] != "lambda" || parts[5] != "function" {
		return false
	}
	if parts[1] != "aws" && parts[1] != "aws-cn" && parts[1] != "aws-us-gov" {
		return false
	}
	if len(parts[3]) < 5 || len(parts[4]) != 12 || parts[6] == "" || len(parts[6]) > 64 {
		return false
	}
	for _, character := range parts[3] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	for _, character := range parts[4] {
		if character < '0' || character > '9' {
			return false
		}
	}
	for _, character := range parts[6] {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	version, err := strconv.ParseUint(parts[7], 10, 64)
	return err == nil && version > 0 && parts[7][0] != '0'
}

func decodeKey(name string) ([]byte, error) {
	value, err := hex.DecodeString(os.Getenv(name))
	if err != nil || len(value) != 32 {
		return nil, fmt.Errorf("%s must be a 32-byte hex key", name)
	}
	return value, nil
}

func requiredAddress(name string) (common.Address, error) {
	value := os.Getenv(name)
	if !common.IsHexAddress(value) || common.HexToAddress(value) == (common.Address{}) {
		return common.Address{}, fmt.Errorf("%s must be a non-zero address", name)
	}
	return common.HexToAddress(value), nil
}

func requiredHash(name string) (common.Hash, error) {
	value, err := normalizeHash(os.Getenv(name))
	if err != nil {
		return common.Hash{}, fmt.Errorf("%s must be a non-zero bytes32 hash", name)
	}
	return common.HexToHash(value), nil
}

func validateRPCs(primary, secondary string) error {
	if primary == secondary {
		return errors.New("primary and secondary RPC endpoints must be independent")
	}
	for _, raw := range []string{primary, secondary} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return errors.New("Robinhood RPC endpoints must be authenticated HTTPS URLs")
		}
		if parsed.Host == "rpc.mainnet.chain.robinhood.com" || parsed.Host == "rpc.testnet.chain.robinhood.com" {
			return errors.New("public Robinhood RPC endpoints are not permitted")
		}
	}
	return nil
}

func validCallerID(value string) bool {
	if len(value) < 3 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
