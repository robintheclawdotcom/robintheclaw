package scheduler

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Enabled        bool
	DatabaseURL    string
	WorkerID       string
	PollInterval   time.Duration
	LeaseDuration  time.Duration
	RequestTimeout time.Duration
	QuoteURL       string
	QuoteCaller    string
	QuoteKey       []byte
	QuotePublicKey ed25519.PublicKey
	RunnerURL      string
	RunnerCaller   string
	RunnerKey      []byte
	LighterMarket  uint32
}

func LoadConfig() (Config, error) {
	enabled, err := strconv.ParseBool(valueOr("ROBIN_LIVE_SCHEDULER_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("ROBIN_LIVE_SCHEDULER_ENABLED must be true or false")
	}
	config := Config{Enabled: enabled, PollInterval: time.Second, LeaseDuration: 15 * time.Second, RequestTimeout: 4 * time.Second}
	if !enabled {
		return config, nil
	}
	config.DatabaseURL = strings.TrimSpace(os.Getenv("ROBIN_LIVE_SCHEDULER_DATABASE_URL"))
	config.WorkerID = strings.TrimSpace(os.Getenv("ROBIN_LIVE_SCHEDULER_WORKER_ID"))
	config.QuoteURL = strings.TrimSpace(os.Getenv("ROBIN_QUOTE_AUTHORITY_URL"))
	config.QuoteCaller = strings.TrimSpace(os.Getenv("ROBIN_LIVE_SCHEDULER_QUOTE_CALLER"))
	config.RunnerURL = strings.TrimSpace(os.Getenv("ROBIN_STRATEGY_RUNNER_URL"))
	config.RunnerCaller = strings.TrimSpace(os.Getenv("ROBIN_LIVE_SCHEDULER_RUNNER_CALLER"))
	market, marketErr := strconv.ParseUint(strings.TrimSpace(os.Getenv("ROBIN_LIVE_SCHEDULER_LIGHTER_AAPL_MARKET_INDEX")), 10, 15)
	if marketErr != nil || market == 0 {
		return Config{}, errors.New("ROBIN_LIVE_SCHEDULER_LIGHTER_AAPL_MARKET_INDEX must pin the reviewed market")
	}
	config.LighterMarket = uint32(market)
	if config.DatabaseURL == "" || config.WorkerID == "" || config.QuoteCaller == "" || config.RunnerCaller == "" {
		return Config{}, errors.New("scheduler database and caller identities are required")
	}
	if !accountPattern.MatchString(config.WorkerID) || config.QuoteCaller == config.RunnerCaller {
		return Config{}, errors.New("worker and distinct caller identities are required")
	}
	if err := validateServiceURL(config.QuoteURL); err != nil {
		return Config{}, fmt.Errorf("quote authority URL: %w", err)
	}
	if err := validateServiceURL(config.RunnerURL); err != nil {
		return Config{}, fmt.Errorf("strategy runner URL: %w", err)
	}
	config.QuoteKey, err = decodeSecret("ROBIN_LIVE_SCHEDULER_QUOTE_HMAC_KEY")
	if err != nil {
		return Config{}, err
	}
	config.RunnerKey, err = decodeSecret("ROBIN_LIVE_SCHEDULER_RUNNER_HMAC_KEY")
	if err != nil {
		return Config{}, err
	}
	if subtle.ConstantTimeCompare(config.QuoteKey, config.RunnerKey) == 1 {
		return Config{}, errors.New("quote and runner HMAC keys must differ")
	}
	publicKey, err := decodeSecret("ROBIN_QUOTE_AUTHORITY_ED25519_PUBLIC_KEY")
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return Config{}, errors.New("ROBIN_QUOTE_AUTHORITY_ED25519_PUBLIC_KEY must be a base64 Ed25519 public key")
	}
	config.QuotePublicKey = ed25519.PublicKey(publicKey)
	return config, nil
}

func decodeSecret(name string) ([]byte, error) {
	value := strings.TrimSpace(os.Getenv(name))
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) < 32 {
		return nil, fmt.Errorf("%s must be base64 and at least 32 bytes", name)
	}
	return decoded, nil
}

func validateServiceURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("must be an absolute HTTP(S) URL without query or fragment")
	}
	return nil
}

func valueOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
