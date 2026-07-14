package sequencerpublisher

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

const chainID uint64 = 4663

var publisherIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,47}$`)

type Config struct {
	PublisherID        string
	SourceRPCURL       string
	TransactionRPCURL  string
	DatabaseURL        string
	ListenAddress      string
	FeedAddress        common.Address
	FeedCodeHash       common.Hash
	PrivateKey         *ecdsa.PrivateKey
	SignerAddress      common.Address
	Interval           time.Duration
	RequestTimeout     time.Duration
	LatestMaxAge       time.Duration
	FinalizedMaxAge    time.Duration
	MaxFinalizedLag    uint64
	MaxGasLimit        uint64
	MaxPriorityFee     *big.Int
	MaxFeePerGas       *big.Int
	MaxTransactionCost *big.Int
	MinimumGasReserve  *big.Int
}

func LoadConfig() (Config, error) {
	config := Config{
		PublisherID:        os.Getenv("SEQUENCER_PUBLISHER_ID"),
		SourceRPCURL:       os.Getenv("SEQUENCER_SOURCE_RPC_URL"),
		TransactionRPCURL:  os.Getenv("SEQUENCER_TRANSACTION_RPC_URL"),
		DatabaseURL:        os.Getenv("SEQUENCER_DATABASE_URL"),
		ListenAddress:      envOr("SEQUENCER_LISTEN_ADDRESS", "127.0.0.1:9090"),
		Interval:           15 * time.Second,
		RequestTimeout:     10 * time.Second,
		LatestMaxAge:       30 * time.Second,
		FinalizedMaxAge:    2 * time.Minute,
		MaxFinalizedLag:    128,
		MaxGasLimit:        150_000,
		MaxPriorityFee:     big.NewInt(100_000_000),
		MaxFeePerGas:       big.NewInt(10_000_000_000),
		MaxTransactionCost: big.NewInt(1_500_000_000_000_000),
		MinimumGasReserve:  big.NewInt(2_000_000_000_000_000),
	}
	if !publisherIDPattern.MatchString(config.PublisherID) {
		return Config{}, errors.New("SEQUENCER_PUBLISHER_ID must be a lowercase service identifier")
	}
	if config.DatabaseURL == "" {
		return Config{}, errors.New("SEQUENCER_DATABASE_URL is required")
	}
	if err := validateIndependentRPCs(config.SourceRPCURL, config.TransactionRPCURL); err != nil {
		return Config{}, err
	}
	if !validAddress(os.Getenv("SEQUENCER_FEED_ADDRESS")) {
		return Config{}, errors.New("SEQUENCER_FEED_ADDRESS must be a nonzero address")
	}
	config.FeedAddress = common.HexToAddress(os.Getenv("SEQUENCER_FEED_ADDRESS"))
	if !validHash(os.Getenv("SEQUENCER_FEED_CODE_HASH")) {
		return Config{}, errors.New("SEQUENCER_FEED_CODE_HASH must be a nonzero bytes32 value")
	}
	config.FeedCodeHash = common.HexToHash(os.Getenv("SEQUENCER_FEED_CODE_HASH"))

	key, err := parsePrivateKey(os.Getenv("SEQUENCER_PUBLISHER_PRIVATE_KEY"))
	if err != nil {
		return Config{}, err
	}
	config.PrivateKey = key
	config.SignerAddress = crypto.PubkeyToAddress(key.PublicKey)

	if config.Interval, err = durationEnv("SEQUENCER_PUBLISH_INTERVAL", config.Interval, 5*time.Second, 20*time.Second); err != nil {
		return Config{}, err
	}
	if config.RequestTimeout, err = durationEnv("SEQUENCER_REQUEST_TIMEOUT", config.RequestTimeout, time.Second, 15*time.Second); err != nil {
		return Config{}, err
	}
	if config.LatestMaxAge, err = durationEnv("SEQUENCER_LATEST_MAX_AGE", config.LatestMaxAge, 5*time.Second, 60*time.Second); err != nil {
		return Config{}, err
	}
	if config.FinalizedMaxAge, err = durationEnv("SEQUENCER_FINALIZED_MAX_AGE", config.FinalizedMaxAge, 30*time.Second, 5*time.Minute); err != nil {
		return Config{}, err
	}
	if config.MaxFinalizedLag, err = uintEnv("SEQUENCER_MAX_FINALIZED_LAG", config.MaxFinalizedLag, 1, 512); err != nil {
		return Config{}, err
	}
	if config.MaxGasLimit, err = uintEnv("SEQUENCER_MAX_GAS_LIMIT", config.MaxGasLimit, 50_000, 250_000); err != nil {
		return Config{}, err
	}
	if config.MaxPriorityFee, err = positiveBigEnv("SEQUENCER_MAX_PRIORITY_FEE_WEI", config.MaxPriorityFee); err != nil {
		return Config{}, err
	}
	if config.MaxFeePerGas, err = positiveBigEnv("SEQUENCER_MAX_FEE_PER_GAS_WEI", config.MaxFeePerGas); err != nil {
		return Config{}, err
	}
	if config.MaxTransactionCost, err = positiveBigEnv("SEQUENCER_MAX_TRANSACTION_COST_WEI", config.MaxTransactionCost); err != nil {
		return Config{}, err
	}
	if config.MinimumGasReserve, err = positiveBigEnv("SEQUENCER_MINIMUM_GAS_RESERVE_WEI", config.MinimumGasReserve); err != nil {
		return Config{}, err
	}
	if config.MaxPriorityFee.Cmp(config.MaxFeePerGas) > 0 {
		return Config{}, errors.New("sequencer priority fee cap exceeds total fee cap")
	}
	maxCost := new(big.Int).Mul(new(big.Int).SetUint64(config.MaxGasLimit), config.MaxFeePerGas)
	if maxCost.Cmp(config.MaxTransactionCost) > 0 {
		return Config{}, errors.New("sequencer gas and fee caps exceed transaction cost cap")
	}
	return config, nil
}

func validateIndependentRPCs(source, transaction string) error {
	sourceURL, err := validateRPCURL(source)
	if err != nil {
		return errors.New("SEQUENCER_SOURCE_RPC_URL must be a private HTTPS endpoint")
	}
	transactionURL, err := validateRPCURL(transaction)
	if err != nil {
		return errors.New("SEQUENCER_TRANSACTION_RPC_URL must be a private HTTPS endpoint")
	}
	if strings.EqualFold(sourceURL.Hostname(), transactionURL.Hostname()) {
		return errors.New("sequencer source and transaction RPCs must use independent hosts")
	}
	return nil
}

func validateRPCURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("invalid RPC URL")
	}
	return parsed, nil
}

func parsePrivateKey(value string) (*ecdsa.PrivateKey, error) {
	value = strings.TrimPrefix(value, "0x")
	encoded, err := hex.DecodeString(value)
	if err != nil || len(encoded) != 32 {
		return nil, errors.New("SEQUENCER_PUBLISHER_PRIVATE_KEY must be a 32-byte hex key")
	}
	key, err := crypto.ToECDSA(encoded)
	if err != nil {
		return nil, errors.New("SEQUENCER_PUBLISHER_PRIVATE_KEY is invalid")
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

func validAddress(value string) bool {
	return common.IsHexAddress(value) && common.HexToAddress(value) != (common.Address{})
}

func validHash(value string) bool {
	return len(value) == 66 && strings.HasPrefix(value, "0x") && common.HexToHash(value) != (common.Hash{})
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
