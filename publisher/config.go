package publisher

import (
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Enabled                     bool
	ListenAddress               string
	PollInterval                time.Duration
	CoordinatorDatabaseURL      string
	RobinhoodDatabaseURL        string
	RobinhoodJournalDatabaseURL string
	PrimaryRPCURL               string
	SecondaryRPCURL             string
	LighterBridge               EndpointConfig
	Coordinator                 EndpointConfig
	Application                 EndpointConfig
	LighterMarketID             uint16
	MinimumCollateralRaw        string
	MinimumSettlementRaw        string
	MinimumOwnerGasRaw          string
	MinimumSignerGasRaw         string
	Environment                 string
}

type EndpointConfig struct {
	URL     string
	Caller  string
	HMACKey string
}

func LoadConfig() (Config, error) {
	enabled := strings.EqualFold(os.Getenv("ACCOUNT_PUBLISHER_ENABLED"), "true")
	listen := os.Getenv("LISTEN_ADDRESS")
	if listen == "" {
		listen = "0.0.0.0:8080"
	}
	if !enabled {
		return Config{Enabled: false, ListenAddress: listen}, nil
	}
	interval := 4500 * time.Millisecond
	if value := os.Getenv("ACCOUNT_PUBLISHER_POLL_MILLISECONDS"); value != "" {
		milliseconds, err := strconv.Atoi(value)
		if err != nil || milliseconds < 4000 || milliseconds > 4500 {
			return Config{}, errors.New("ACCOUNT_PUBLISHER_POLL_MILLISECONDS must be between 4000 and 4500")
		}
		interval = time.Duration(milliseconds) * time.Millisecond
	}
	marketID, err := strconv.ParseUint(os.Getenv("ACCOUNT_PUBLISHER_LIGHTER_MARKET_ID"), 10, 16)
	if err != nil || marketID == 0 || marketID >= 255 {
		return Config{}, errors.New("ACCOUNT_PUBLISHER_LIGHTER_MARKET_ID must be between 1 and 254")
	}
	config := Config{
		Enabled: true, ListenAddress: listen, PollInterval: interval,
		CoordinatorDatabaseURL:      os.Getenv("ACCOUNT_PUBLISHER_COORDINATOR_DATABASE_URL"),
		RobinhoodDatabaseURL:        os.Getenv("ACCOUNT_PUBLISHER_ROBINHOOD_DATABASE_URL"),
		RobinhoodJournalDatabaseURL: os.Getenv("ACCOUNT_PUBLISHER_ROBINHOOD_JOURNAL_DATABASE_URL"),
		PrimaryRPCURL:               os.Getenv("ACCOUNT_PUBLISHER_PRIMARY_RPC_URL"),
		SecondaryRPCURL:             os.Getenv("ACCOUNT_PUBLISHER_SECONDARY_RPC_URL"),
		LighterBridge: EndpointConfig{
			URL: os.Getenv("ACCOUNT_PUBLISHER_LIGHTER_BRIDGE_URL"), Caller: os.Getenv("LIGHTER_PUBLISHER_BRIDGE_CALLER_ID"),
			HMACKey: os.Getenv("LIGHTER_PUBLISHER_BRIDGE_HMAC_KEY"),
		},
		Coordinator: EndpointConfig{
			URL: os.Getenv("ACCOUNT_PUBLISHER_COORDINATOR_URL"), Caller: os.Getenv("ACCOUNT_PUBLISHER_COORDINATOR_CALLER_ID"),
			HMACKey: os.Getenv("ACCOUNT_PUBLISHER_COORDINATOR_HMAC_KEY"),
		},
		Application: EndpointConfig{
			URL: os.Getenv("ACCOUNT_PUBLISHER_APPLICATION_URL"), Caller: os.Getenv("ACCOUNT_PUBLISHER_APPLICATION_CALLER_ID"),
			HMACKey: os.Getenv("ACCOUNT_PUBLISHER_APPLICATION_HMAC_KEY"),
		},
		LighterMarketID:      uint16(marketID),
		MinimumCollateralRaw: os.Getenv("ACCOUNT_PUBLISHER_MINIMUM_COLLATERAL_RAW"),
		MinimumSettlementRaw: os.Getenv("ACCOUNT_PUBLISHER_MINIMUM_SETTLEMENT_RAW"),
		MinimumOwnerGasRaw:   os.Getenv("ACCOUNT_PUBLISHER_MINIMUM_OWNER_GAS_RAW"),
		MinimumSignerGasRaw:  os.Getenv("ACCOUNT_PUBLISHER_MINIMUM_SIGNER_GAS_RAW"),
		Environment:          os.Getenv("ACCOUNT_PUBLISHER_ENVIRONMENT"),
	}
	if err := validateConfig(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func validateConfig(config Config) error {
	if config.CoordinatorDatabaseURL == "" || config.RobinhoodDatabaseURL == "" || config.RobinhoodJournalDatabaseURL == "" {
		return errors.New("publisher read-only database URLs are required")
	}
	if !validMetricLabel(config.Environment) {
		return errors.New("ACCOUNT_PUBLISHER_ENVIRONMENT must be a lowercase environment label")
	}
	if config.LighterMarketID == 0 || !decimalAtLeast(config.MinimumCollateralRaw, "50") ||
		!decimalAtLeast(config.MinimumSettlementRaw, "25000000") ||
		!decimalAtLeast(config.MinimumOwnerGasRaw, "1") || !decimalAtLeast(config.MinimumSignerGasRaw, "1") {
		return errors.New("publisher market or minimums are unsafe")
	}
	keys := make(map[string]struct{}, 3)
	for _, endpoint := range []EndpointConfig{config.LighterBridge, config.Coordinator, config.Application} {
		if endpoint.URL == "" || endpoint.Caller == "" || endpoint.HMACKey == "" {
			return errors.New("publisher signed endpoints are required")
		}
		decoded, err := hex.DecodeString(endpoint.HMACKey)
		if err != nil || len(decoded) != 32 || endpoint.HMACKey != strings.ToLower(endpoint.HMACKey) {
			return errors.New("publisher HMAC keys must be 32-byte lowercase hex")
		}
		if _, exists := keys[endpoint.HMACKey]; exists {
			return errors.New("publisher HMAC keys must be distinct")
		}
		keys[endpoint.HMACKey] = struct{}{}
	}
	return nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func validExecutionID(value string) bool {
	if len(value) < 8 || len(value) > 64 ||
		!((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= '0' && value[0] <= '9')) {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}
