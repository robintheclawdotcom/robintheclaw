package evaluation

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var workerPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,63}$`)

type Config struct {
	Enabled           bool
	ResearchDatabase  string
	ProductDatabase   string
	ExecutionDatabase string
	WorkerID          string
	PollInterval      time.Duration
	ApprovalLifetime  time.Duration
	MinimumNetEdgePPM uint64
	LighterMarket     uint32
	MarketBootstrap   MarketBootstrapConfig
}

func LoadConfig() (Config, error) {
	enabled, err := strconv.ParseBool(valueOr("ROBIN_LIVE_EVALUATION_ENABLED", "false"))
	if err != nil {
		return Config{}, errors.New("ROBIN_LIVE_EVALUATION_ENABLED must be true or false")
	}
	config := Config{Enabled: enabled, PollInterval: 250 * time.Millisecond, ApprovalLifetime: 4 * time.Second}
	if !enabled {
		return config, nil
	}
	config.ResearchDatabase = strings.TrimSpace(os.Getenv("ROBIN_LIVE_EVALUATION_RESEARCH_DATABASE_URL"))
	config.ProductDatabase = strings.TrimSpace(os.Getenv("ROBIN_LIVE_EVALUATION_PRODUCT_DATABASE_URL"))
	config.ExecutionDatabase = strings.TrimSpace(os.Getenv("ROBIN_LIVE_EVALUATION_EXECUTION_DATABASE_URL"))
	config.WorkerID = strings.TrimSpace(os.Getenv("ROBIN_LIVE_EVALUATION_WORKER_ID"))
	if value := strings.TrimSpace(os.Getenv("ROBIN_LIVE_EVALUATION_POLL_MILLISECONDS")); value != "" {
		milliseconds, parseErr := strconv.ParseUint(value, 10, 16)
		if parseErr != nil || milliseconds < 100 || milliseconds > 1_000 {
			return Config{}, errors.New("ROBIN_LIVE_EVALUATION_POLL_MILLISECONDS must be between 100 and 1000")
		}
		config.PollInterval = time.Duration(milliseconds) * time.Millisecond
	}
	edge, err := strconv.ParseUint(strings.TrimSpace(os.Getenv("AAPL_MINIMUM_NET_EDGE_PPM")), 10, 32)
	if err != nil || edge == 0 || edge > 1_000_000 {
		return Config{}, errors.New("AAPL_MINIMUM_NET_EDGE_PPM must be between 1 and 1000000")
	}
	if err := verifyStrategyPolicy(edge, strings.TrimSpace(os.Getenv("AAPL_STRATEGY_POLICY_SALT"))); err != nil {
		return Config{}, err
	}
	config.MinimumNetEdgePPM = edge
	market, err := strconv.ParseUint(strings.TrimSpace(os.Getenv("ROBIN_LIVE_EVALUATION_LIGHTER_AAPL_MARKET_INDEX")), 10, 15)
	if err != nil || market == 0 {
		return Config{}, errors.New("ROBIN_LIVE_EVALUATION_LIGHTER_AAPL_MARKET_INDEX must pin the reviewed market")
	}
	config.LighterMarket = uint32(market)
	config.MarketBootstrap, err = loadMarketBootstrapConfig(config.LighterMarket)
	if err != nil {
		return Config{}, err
	}
	if !workerPattern.MatchString(config.WorkerID) {
		return Config{}, errors.New("ROBIN_LIVE_EVALUATION_WORKER_ID is invalid")
	}
	for name, raw := range map[string]string{
		"research database":  config.ResearchDatabase,
		"product database":   config.ProductDatabase,
		"execution database": config.ExecutionDatabase,
	} {
		if err := validateDatabaseURL(raw); err != nil {
			return Config{}, fmt.Errorf("%s: %w", name, err)
		}
	}
	researchID := databaseIdentity(config.ResearchDatabase)
	productID := databaseIdentity(config.ProductDatabase)
	executionID := databaseIdentity(config.ExecutionDatabase)
	if researchID == productID || researchID == executionID || productID == executionID {
		return Config{}, errors.New("live evaluation databases must be distinct")
	}
	return config, nil
}

func loadMarketBootstrapConfig(market uint32) (MarketBootstrapConfig, error) {
	baseDecimals, err := requiredUint8("ROBIN_LIVE_EVALUATION_LIGHTER_AAPL_BASE_DECIMALS", 18)
	if err != nil {
		return MarketBootstrapConfig{}, err
	}
	priceDecimals, err := requiredUint8("ROBIN_LIVE_EVALUATION_LIGHTER_AAPL_PRICE_DECIMALS", 18)
	if err != nil {
		return MarketBootstrapConfig{}, err
	}
	spotVersion, err := strconv.ParseUint(strings.TrimSpace(os.Getenv("ROBIN_LIVE_EVALUATION_SPOT_CONFIG_VERSION")), 10, 63)
	if err != nil || spotVersion == 0 {
		return MarketBootstrapConfig{}, errors.New("ROBIN_LIVE_EVALUATION_SPOT_CONFIG_VERSION must pin the onchain AAPL config")
	}
	multiplier := strings.TrimSpace(os.Getenv("ROBIN_LIVE_EVALUATION_UI_MULTIPLIER_E18"))
	if !decimalPattern.MatchString(multiplier) || multiplier == "0" || len(multiplier) > 39 {
		return MarketBootstrapConfig{}, errors.New("ROBIN_LIVE_EVALUATION_UI_MULTIPLIER_E18 must pin the onchain AAPL multiplier")
	}
	priceDeviation, err := requiredBPS("ROBIN_LIVE_EVALUATION_MAX_PRICE_DEVIATION_BPS", 500)
	if err != nil {
		return MarketBootstrapConfig{}, err
	}
	unwindDeviation, err := requiredBPS("ROBIN_LIVE_EVALUATION_MAX_UNWIND_PRICE_DEVIATION_BPS", 5_000)
	if err != nil {
		return MarketBootstrapConfig{}, err
	}
	validFrom, err := requiredTime("ROBIN_LIVE_EVALUATION_MARKET_VALID_FROM")
	if err != nil {
		return MarketBootstrapConfig{}, err
	}
	validUntil, err := requiredTime("ROBIN_LIVE_EVALUATION_MARKET_VALID_UNTIL")
	if err != nil || !validUntil.After(validFrom) {
		return MarketBootstrapConfig{}, errors.New("ROBIN_LIVE_EVALUATION_MARKET_VALID_UNTIL must follow MARKET_VALID_FROM")
	}
	return MarketBootstrapConfig{
		ExpectedMarketIndex: market, ExpectedBaseDecimals: baseDecimals,
		ExpectedPriceDecimals: priceDecimals, SpotConfigVersion: spotVersion,
		UIMultiplierE18: multiplier, MaxPriceDeviationBPS: priceDeviation,
		MaxUnwindPriceDeviationBPS: unwindDeviation, ValidFrom: validFrom, ValidUntil: validUntil,
	}, nil
}

func requiredUint8(name string, maximum uint8) (uint8, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(os.Getenv(name)), 10, 8)
	if err != nil || value > uint64(maximum) {
		return 0, fmt.Errorf("%s must be between 0 and %d", name, maximum)
	}
	return uint8(value), nil
}

func requiredBPS(name string, maximum uint16) (uint16, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(os.Getenv(name)), 10, 16)
	if err != nil || value == 0 || value > uint64(maximum) {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, maximum)
	}
	return uint16(value), nil
}

func requiredTime(name string) (time.Time, error) {
	value, err := time.Parse(time.RFC3339, strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339", name)
	}
	return value.UTC().Truncate(time.Millisecond), nil
}

func databaseIdentity(raw string) string {
	parsed, _ := url.Parse(raw)
	return strings.ToLower(parsed.Host) + parsed.EscapedPath()
}

func validateDatabaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return errors.New("must be an absolute PostgreSQL URL")
	}
	return nil
}

func valueOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
