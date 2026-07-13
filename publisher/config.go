package publisher

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Enabled         bool
	ListenAddress   string
	PollInterval    time.Duration
	LighterURL      string
	PrimaryRPCURL   string
	SecondaryRPCURL string
	Coordinator     EndpointConfig
	Application     EndpointConfig
	Accounts        []AccountBinding
}

type EndpointConfig struct {
	URL     string `json:"url"`
	Caller  string `json:"caller"`
	KeyFile string `json:"keyFile"`
}

type configFile struct {
	LighterURL      string           `json:"lighterUrl"`
	PrimaryRPCURL   string           `json:"primaryRpcUrl"`
	SecondaryRPCURL string           `json:"secondaryRpcUrl"`
	Coordinator     EndpointConfig   `json:"coordinator"`
	Application     EndpointConfig   `json:"application"`
	Accounts        []AccountBinding `json:"accounts"`
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
	path := os.Getenv("ACCOUNT_PUBLISHER_CONFIG_FILE")
	if path == "" {
		return Config{}, errors.New("ACCOUNT_PUBLISHER_CONFIG_FILE is required when enabled")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read publisher config: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var file configFile
	if err := decoder.Decode(&file); err != nil {
		return Config{}, errors.New("invalid publisher config")
	}
	interval := 4500 * time.Millisecond
	if value := os.Getenv("ACCOUNT_PUBLISHER_POLL_MILLISECONDS"); value != "" {
		milliseconds, err := strconv.Atoi(value)
		if err != nil || milliseconds < 4000 || milliseconds > 4500 {
			return Config{}, errors.New("ACCOUNT_PUBLISHER_POLL_MILLISECONDS must be between 4000 and 4500")
		}
		interval = time.Duration(milliseconds) * time.Millisecond
	}
	config := Config{
		Enabled: true, ListenAddress: listen, PollInterval: interval,
		LighterURL: file.LighterURL, PrimaryRPCURL: file.PrimaryRPCURL, SecondaryRPCURL: file.SecondaryRPCURL,
		Coordinator: file.Coordinator, Application: file.Application, Accounts: file.Accounts,
	}
	if err := validateConfig(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func validateConfig(config Config) error {
	if config.LighterURL != lighterMainnetURL || len(config.Accounts) == 0 {
		return errors.New("publisher must use the pinned Lighter mainnet API and at least one account")
	}
	ids := make(map[string]struct{})
	lighterAccounts := make(map[uint64]struct{})
	vaults := make(map[string]struct{})
	tokenFiles := make(map[string]struct{})
	for _, account := range config.Accounts {
		if !validExecutionID(account.ExecutionAccountID) || account.Lighter.MarketID == 0 ||
			!decimalAtLeast(account.Lighter.MinimumCollateralRaw, "50") {
			return errors.New("invalid account binding")
		}
		if validUUID(account.ExecutionAccountID) {
			if account.ReadinessAccountID != account.ExecutionAccountID {
				return errors.New("product readiness account must match the execution account")
			}
		} else if account.ExecutionAccountID != "singleton-mainnet-canary" || account.ReadinessAccountID != "" {
			return errors.New("only the internal singleton may omit product readiness publication")
		}
		if _, exists := ids[account.ExecutionAccountID]; exists {
			return errors.New("duplicate execution account binding")
		}
		if _, exists := lighterAccounts[account.Lighter.AccountIndex]; exists {
			return errors.New("cross-account Lighter binding")
		}
		vault := strings.ToLower(account.Robinhood.Vault)
		if _, exists := vaults[vault]; exists {
			return errors.New("cross-account vault binding")
		}
		if _, exists := tokenFiles[account.Lighter.ReadOnlyTokenFile]; exists {
			return errors.New("cross-account token binding")
		}
		if err := validateRobinhoodBinding(account.Robinhood); err != nil {
			return err
		}
		ids[account.ExecutionAccountID] = struct{}{}
		lighterAccounts[account.Lighter.AccountIndex] = struct{}{}
		vaults[vault] = struct{}{}
		tokenFiles[account.Lighter.ReadOnlyTokenFile] = struct{}{}
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
