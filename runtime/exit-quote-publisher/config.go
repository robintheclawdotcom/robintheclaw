package exitquote

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
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
	RequestTimeout time.Duration
	QuoteURL       string
	QuoteCaller    string
	QuoteKey       []byte
	QuotePublicKey ed25519.PublicKey
	LighterMarket  uint32
}

func LoadConfig() (Config, error) {
	enabled, err := strconv.ParseBool(valueOr("ROBIN_EXIT_QUOTE_PUBLISHER_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("ROBIN_EXIT_QUOTE_PUBLISHER_ENABLED must be true or false")
	}
	config := Config{
		Enabled: enabled, PollInterval: time.Second, RequestTimeout: 4 * time.Second,
	}
	if !enabled {
		return config, nil
	}
	config.DatabaseURL = strings.TrimSpace(os.Getenv("ROBIN_EXIT_QUOTE_PUBLISHER_DATABASE_URL"))
	config.WorkerID = strings.TrimSpace(os.Getenv("ROBIN_EXIT_QUOTE_PUBLISHER_WORKER_ID"))
	config.QuoteURL = strings.TrimSpace(os.Getenv("ROBIN_QUOTE_AUTHORITY_URL"))
	config.QuoteCaller = strings.TrimSpace(os.Getenv("ROBIN_EXIT_QUOTE_PUBLISHER_CALLER"))
	if config.DatabaseURL == "" || !accountPattern.MatchString(config.WorkerID) || !accountPattern.MatchString(config.QuoteCaller) {
		return Config{}, errors.New("publisher database, worker, and caller identities are required")
	}
	if err := validateServiceURL(config.QuoteURL); err != nil {
		return Config{}, fmt.Errorf("quote authority URL: %w", err)
	}
	config.QuoteKey, err = decodeBase64("ROBIN_EXIT_QUOTE_PUBLISHER_HMAC_KEY")
	if err != nil || len(config.QuoteKey) < 32 {
		return Config{}, errors.New("ROBIN_EXIT_QUOTE_PUBLISHER_HMAC_KEY must be at least 32 bytes of base64")
	}
	publicKey, err := decodeBase64("ROBIN_QUOTE_AUTHORITY_ED25519_PUBLIC_KEY")
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return Config{}, errors.New("ROBIN_QUOTE_AUTHORITY_ED25519_PUBLIC_KEY must be a base64 Ed25519 public key")
	}
	config.QuotePublicKey = ed25519.PublicKey(publicKey)
	market, err := strconv.ParseUint(strings.TrimSpace(os.Getenv("ROBIN_EXIT_QUOTE_PUBLISHER_LIGHTER_AAPL_MARKET_INDEX")), 10, 15)
	if err != nil || market == 0 {
		return Config{}, errors.New("ROBIN_EXIT_QUOTE_PUBLISHER_LIGHTER_AAPL_MARKET_INDEX must pin the reviewed market")
	}
	config.LighterMarket = uint32(market)
	return config, nil
}

func validateServiceURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return errors.New("must be an absolute service URL")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme != "http" || !privateHost(parsed.Hostname()) {
		return errors.New("must use HTTPS or private-network HTTP")
	}
	return nil
}

func privateHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".internal") || accountPattern.MatchString(host) {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && (address.IsLoopback() || address.IsPrivate())
}

func decodeBase64(name string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv(name)))
}

func valueOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
