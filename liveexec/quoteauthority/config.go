package quoteauthority

import (
	"crypto/ed25519"
	"crypto/hmac"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Enabled                  bool
	ListenAddress            string
	Caller                   string
	AuthKey                  []byte
	ExitCaller               string
	ExitAuthKey              []byte
	QuoteSigningKey          ed25519.PrivateKey
	CoordinatorURL           string
	CoordinatorCaller        string
	CoordinatorKey           []byte
	CoordinatorEpisodeCaller string
	CoordinatorEpisodeKey    []byte
	LighterMarketIndex       uint32
	Adapter                  LiveAdapterConfig
}

func LoadConfig() (Config, error) {
	enabled, err := strconv.ParseBool(valueOrDefault("ROBIN_QUOTE_AUTHORITY_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("ROBIN_QUOTE_AUTHORITY_ENABLED must be true or false")
	}
	config := Config{Enabled: enabled, ListenAddress: valueOrDefault("ROBIN_QUOTE_AUTHORITY_LISTEN", ":8080")}
	if !enabled {
		return config, nil
	}
	config.Caller = os.Getenv("ROBIN_QUOTE_AUTHORITY_CALLER")
	config.AuthKey, err = decodeBase64("ROBIN_QUOTE_AUTHORITY_HMAC_KEY")
	if err != nil || len(config.AuthKey) < 32 {
		return Config{}, errors.New("ROBIN_QUOTE_AUTHORITY_HMAC_KEY must be at least 32 bytes of base64")
	}
	config.ExitCaller = os.Getenv("ROBIN_QUOTE_AUTHORITY_EXIT_CALLER")
	config.ExitAuthKey, err = decodeBase64("ROBIN_QUOTE_AUTHORITY_EXIT_HMAC_KEY")
	if err != nil || len(config.ExitAuthKey) < 32 {
		return Config{}, errors.New("ROBIN_QUOTE_AUTHORITY_EXIT_HMAC_KEY must be at least 32 bytes of base64")
	}
	key, err := decodeBase64("ROBIN_QUOTE_AUTHORITY_ED25519_PRIVATE_KEY")
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return Config{}, errors.New("ROBIN_QUOTE_AUTHORITY_ED25519_PRIVATE_KEY must be a base64 Ed25519 private key")
	}
	config.QuoteSigningKey = ed25519.PrivateKey(key)
	config.CoordinatorURL = os.Getenv("ROBIN_COORDINATOR_URL")
	config.CoordinatorCaller = os.Getenv("ROBIN_COORDINATOR_MARKET_CALLER")
	config.CoordinatorKey, err = decodeHexKey("ROBIN_COORDINATOR_MARKET_HMAC_KEY")
	if err != nil {
		return Config{}, err
	}
	config.CoordinatorEpisodeCaller = os.Getenv("ROBIN_COORDINATOR_EPISODE_CALLER")
	config.CoordinatorEpisodeKey, err = decodeHexKey("ROBIN_COORDINATOR_EPISODE_HMAC_KEY")
	if err != nil {
		return Config{}, err
	}
	if _, err := coordinatorEndpoint(config.CoordinatorURL, "/v1/market-quotes"); err != nil {
		return Config{}, err
	}
	callers := []string{config.Caller, config.ExitCaller, config.CoordinatorCaller, config.CoordinatorEpisodeCaller}
	for index, caller := range callers {
		if !validCaller(caller) {
			return Config{}, errors.New("quote authority callers are invalid")
		}
		for other := 0; other < index; other++ {
			if callers[other] == caller {
				return Config{}, errors.New("quote authority callers must be distinct")
			}
		}
	}
	keys := [][]byte{config.AuthKey, config.ExitAuthKey, config.CoordinatorKey, config.CoordinatorEpisodeKey}
	for index, key := range keys {
		for other := 0; other < index; other++ {
			if hmac.Equal(keys[other], key) {
				return Config{}, errors.New("quote authority HMAC keys must be distinct")
			}
		}
	}

	marketIndex, err := requiredUint("ROBIN_LIGHTER_AAPL_MARKET_INDEX", 15)
	if err != nil {
		return Config{}, errors.New("ROBIN_LIGHTER_AAPL_MARKET_INDEX must be an explicitly reviewed index between 0 and 32767")
	}
	baseDecimals, err := requiredUint("LIGHTER_AAPL_BASE_DECIMALS", 8)
	if err != nil || baseDecimals > 18 {
		return Config{}, errors.New("LIGHTER_AAPL_BASE_DECIMALS must be explicitly reviewed between 0 and 18")
	}
	priceDecimals, err := requiredUint("LIGHTER_AAPL_PRICE_DECIMALS", 8)
	if err != nil || priceDecimals > 18 {
		return Config{}, errors.New("LIGHTER_AAPL_PRICE_DECIMALS must be explicitly reviewed between 0 and 18")
	}
	feedDecimals, err := requiredUint("AAPL_REFERENCE_FEED_DECIMALS", 8)
	if err != nil || feedDecimals > 18 {
		return Config{}, errors.New("AAPL_REFERENCE_FEED_DECIMALS must be explicitly reviewed between 0 and 18")
	}
	heartbeatSeconds, err := requiredUint("AAPL_REFERENCE_FEED_HEARTBEAT_SECONDS", 32)
	if err != nil {
		return Config{}, errors.New("AAPL_REFERENCE_FEED_HEARTBEAT_SECONDS is required")
	}
	config.LighterMarketIndex = uint32(marketIndex)
	config.Adapter = LiveAdapterConfig{
		PrimaryRPCURL:          os.Getenv("ROBINHOOD_RPC_URL"),
		SecondaryRPCURL:        os.Getenv("ROBINHOOD_RECONCILIATION_RPC_URL"),
		LighterAPIURL:          valueOrDefault("LIGHTER_API_URL", "https://mainnet.zklighter.elliot.ai"),
		ReferenceFeed:          os.Getenv("AAPL_REFERENCE_FEED"),
		ReferenceFeedCodeHash:  os.Getenv("AAPL_REFERENCE_FEED_CODE_HASH"),
		ReferenceFeedDecimals:  uint8(feedDecimals),
		ReferenceFeedHeartbeat: time.Duration(heartbeatSeconds) * time.Second,
		LighterMarketIndex:     uint32(marketIndex),
		LighterBaseDecimals:    uint8(baseDecimals),
		LighterPriceDecimals:   uint8(priceDecimals),
	}
	if err := config.Adapter.validate(true); err != nil {
		return Config{}, fmt.Errorf("live quote adapter: %w", err)
	}
	return config, nil
}

func decodeBase64(name string) ([]byte, error) {
	value := os.Getenv(name)
	if value == "" {
		return nil, errors.New("missing environment value")
	}
	return base64.StdEncoding.DecodeString(value)
}

func decodeHexKey(name string) ([]byte, error) {
	encoded := os.Getenv(name)
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != 32 || encoded != hex.EncodeToString(decoded) {
		return nil, fmt.Errorf("%s must be a 32-byte lowercase hex value", name)
	}
	return decoded, nil
}

func requiredUint(name string, bits int) (uint64, error) {
	value := os.Getenv(name)
	if value == "" {
		return 0, errors.New("missing environment value")
	}
	return strconv.ParseUint(value, 10, bits)
}

func valueOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
