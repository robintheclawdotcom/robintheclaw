package aaplrelay

import (
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"math/big"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	SourceChainID  uint64 = 42161
	TargetChainID  uint64 = 4663
	SourceDecimals uint8  = 8
	MaxSourceAge          = 25 * time.Hour
	MaxReportAge          = 60 * time.Second
	SourceFeedHex         = "0x8d0CC5f38f9E802475f2CFf4F9fc7000C2E1557c"
)

var publisherIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,47}$`)

type Config struct {
	PublisherID        string
	SourceRPC1         string
	SourceRPC2         string
	TargetRPC          string
	DatabaseURL        string
	ListenAddress      string
	SourceFeed         common.Address
	SourceCodeHash     common.Hash
	TargetFeed         common.Address
	TargetCodeHash     common.Hash
	PrivateKey         *ecdsa.PrivateKey
	SignerAddress      common.Address
	Interval           time.Duration
	RequestTimeout     time.Duration
	FinalizedMaxAge    time.Duration
	MaxGasLimit        uint64
	MaxPriorityFee     *big.Int
	MaxFeePerGas       *big.Int
	MaxTransactionCost *big.Int
	MinimumGasReserve  *big.Int
}

func LoadConfig() (Config, error) {
	config := Config{
		PublisherID:        os.Getenv("AAPL_RELAY_PUBLISHER_ID"),
		SourceRPC1:         os.Getenv("AAPL_RELAY_ARBITRUM_RPC_1"),
		SourceRPC2:         os.Getenv("AAPL_RELAY_ARBITRUM_RPC_2"),
		TargetRPC:          os.Getenv("AAPL_RELAY_ROBINHOOD_RPC"),
		DatabaseURL:        os.Getenv("AAPL_RELAY_DATABASE_URL"),
		ListenAddress:      envOr("AAPL_RELAY_LISTEN_ADDRESS", "127.0.0.1:9090"),
		SourceFeed:         common.HexToAddress(SourceFeedHex),
		Interval:           15 * time.Second,
		RequestTimeout:     12 * time.Second,
		FinalizedMaxAge:    20 * time.Minute,
		MaxGasLimit:        180_000,
		MaxPriorityFee:     big.NewInt(100_000_000),
		MaxFeePerGas:       big.NewInt(10_000_000_000),
		MaxTransactionCost: big.NewInt(1_800_000_000_000_000),
		MinimumGasReserve:  big.NewInt(2_000_000_000_000_000),
	}
	if !publisherIDPattern.MatchString(config.PublisherID) {
		return Config{}, errors.New("AAPL_RELAY_PUBLISHER_ID must be a lowercase service identifier")
	}
	if config.DatabaseURL == "" {
		return Config{}, errors.New("AAPL_RELAY_DATABASE_URL is required")
	}
	if err := validateIndependentRPCs(config.SourceRPC1, config.SourceRPC2, config.TargetRPC); err != nil {
		return Config{}, err
	}
	var err error
	if config.SourceCodeHash, err = requiredHash("AAPL_SOURCE_FEED_CODE_HASH"); err != nil {
		return Config{}, err
	}
	if config.TargetFeed, err = requiredAddress("AAPL_REFERENCE_FEED"); err != nil {
		return Config{}, err
	}
	if config.TargetCodeHash, err = requiredHash("AAPL_REFERENCE_FEED_CODE_HASH"); err != nil {
		return Config{}, err
	}
	config.PrivateKey, err = parsePrivateKey(os.Getenv("AAPL_RELAY_PUBLISHER_PRIVATE_KEY"))
	if err != nil {
		return Config{}, err
	}
	config.SignerAddress = crypto.PubkeyToAddress(config.PrivateKey.PublicKey)

	if config.Interval, err = durationEnv("AAPL_RELAY_INTERVAL", config.Interval, 5*time.Second, 20*time.Second); err != nil {
		return Config{}, err
	}
	if config.RequestTimeout, err = durationEnv("AAPL_RELAY_REQUEST_TIMEOUT", config.RequestTimeout, 2*time.Second, 20*time.Second); err != nil {
		return Config{}, err
	}
	if config.FinalizedMaxAge, err = durationEnv("AAPL_RELAY_FINALIZED_MAX_AGE", config.FinalizedMaxAge, time.Minute, time.Hour); err != nil {
		return Config{}, err
	}
	if config.MaxGasLimit, err = uintEnv("AAPL_RELAY_MAX_GAS_LIMIT", config.MaxGasLimit, 80_000, 300_000); err != nil {
		return Config{}, err
	}
	if config.MaxPriorityFee, err = positiveBigEnv("AAPL_RELAY_MAX_PRIORITY_FEE_WEI", config.MaxPriorityFee); err != nil {
		return Config{}, err
	}
	if config.MaxFeePerGas, err = positiveBigEnv("AAPL_RELAY_MAX_FEE_PER_GAS_WEI", config.MaxFeePerGas); err != nil {
		return Config{}, err
	}
	if config.MaxTransactionCost, err = positiveBigEnv("AAPL_RELAY_MAX_TRANSACTION_COST_WEI", config.MaxTransactionCost); err != nil {
		return Config{}, err
	}
	if config.MinimumGasReserve, err = positiveBigEnv("AAPL_RELAY_MINIMUM_GAS_RESERVE_WEI", config.MinimumGasReserve); err != nil {
		return Config{}, err
	}
	if config.MaxPriorityFee.Cmp(config.MaxFeePerGas) > 0 {
		return Config{}, errors.New("AAPL relay priority fee cap exceeds total fee cap")
	}
	maxCost := new(big.Int).Mul(new(big.Int).SetUint64(config.MaxGasLimit), config.MaxFeePerGas)
	if maxCost.Cmp(config.MaxTransactionCost) > 0 {
		return Config{}, errors.New("AAPL relay gas and fee caps exceed transaction cost cap")
	}
	return config, nil
}

func validateIndependentRPCs(values ...string) error {
	hosts := make(map[string]struct{}, len(values))
	for _, value := range values {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			return errors.New("AAPL relay RPCs must be private HTTPS endpoints")
		}
		host := strings.ToLower(parsed.Hostname())
		if _, exists := hosts[host]; exists {
			return errors.New("AAPL relay RPCs must use independent hosts")
		}
		hosts[host] = struct{}{}
	}
	return nil
}

func requiredAddress(name string) (common.Address, error) {
	value := os.Getenv(name)
	if !common.IsHexAddress(value) || common.HexToAddress(value) == (common.Address{}) {
		return common.Address{}, errors.New(name + " must be a nonzero address")
	}
	return common.HexToAddress(value), nil
}

func requiredHash(name string) (common.Hash, error) {
	value := os.Getenv(name)
	if len(value) != 66 || !strings.HasPrefix(value, "0x") || common.HexToHash(value) == (common.Hash{}) {
		return common.Hash{}, errors.New(name + " must be a nonzero bytes32 value")
	}
	return common.HexToHash(value), nil
}

func parsePrivateKey(value string) (*ecdsa.PrivateKey, error) {
	encoded, err := hex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil || len(encoded) != 32 {
		return nil, errors.New("AAPL_RELAY_PUBLISHER_PRIVATE_KEY must be a 32-byte hex key")
	}
	key, err := crypto.ToECDSA(encoded)
	if err != nil {
		return nil, errors.New("AAPL_RELAY_PUBLISHER_PRIVATE_KEY is invalid")
	}
	return key, nil
}

func durationEnv(name string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New(name + " is outside its allowed range")
	}
	return parsed, nil
}

func uintEnv(name string, fallback, minimum, maximum uint64) (uint64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New(name + " is outside its allowed range")
	}
	return parsed, nil
}

func positiveBigEnv(name string, fallback *big.Int) (*big.Int, error) {
	value := os.Getenv(name)
	if value == "" {
		return new(big.Int).Set(fallback), nil
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok || parsed.Sign() <= 0 {
		return nil, errors.New(name + " must be a positive integer")
	}
	return parsed, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
