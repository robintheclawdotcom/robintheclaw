package evaluation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	scheduler "github.com/robin-the-claw/live-scheduler"
)

const (
	lighterMainnetEndpoint = "https://mainnet.zklighter.elliot.ai"
	marketBootstrapLock    = int64(5_021_474_677_824_812_391)
	maximumMetadataBytes   = 2 << 20
	maximumMetadataAge     = 30 * time.Second
	marketSpotDecimals     = uint8(18)
	marketSpotSlippageBPS  = uint16(200)
	marketSpotVersion      = uint8(1)
	marketChainID          = uint64(4663)
	marketPoolFee          = uint32(10_000)
	marketPoolTickSpacing  = int32(200)
	marketSettlementToken  = "0x5fc5360d0400a0fd4f2af552add042d716f1d168"
	marketRouter           = "0x8876789976decbfcbbbe364623c63652db8c0904"
	marketPermit2          = "0x000000000022d473030f116ddee9f6b43ac78ba3"
	marketPoolManager      = "0x8366a39cc670b4001a1121b8f6a443a643e40951"
	marketHooks            = "0x0000000000000000000000000000000000000000"
	marketPoolID           = "0xda4116b5894ee7479e64eae9276e1a2944ef0e5ce863a299d296a15618deee01"
)

type MarketBootstrapConfig struct {
	ExpectedMarketIndex        uint32
	ExpectedBaseDecimals       uint8
	ExpectedPriceDecimals      uint8
	SpotConfigVersion          uint64
	UIMultiplierE18            string
	MaxPriceDeviationBPS       uint16
	MaxUnwindPriceDeviationBPS uint16
	ValidFrom                  time.Time
	ValidUntil                 time.Time
}

type LighterMarketMetadata struct {
	Symbol         string
	MarketIndex    uint32
	MarketType     string
	Status         string
	BaseAssetID    uint32
	QuoteAssetID   uint32
	BaseDecimals   uint8
	PriceDecimals  uint8
	QuoteDecimals  uint8
	ResponseSHA256 string
	ObservedAt     time.Time
}

type MarketMetadataSource interface {
	Discover(context.Context, MarketBootstrapConfig) (LighterMarketMetadata, error)
}

type MarketConfigWriter interface {
	EnsureMarketConfig(context.Context, MarketConfigRecord) (bool, error)
}

type MarketConfigRecord struct {
	Config             MarketConfig
	Review             MarketReview
	MetadataSHA256     string
	MetadataObservedAt time.Time
}

type MarketReview struct {
	SchemaVersion          uint8  `json:"schema_version"`
	StrategyVersion        string `json:"strategy_version"`
	StrategyManifestSHA256 string `json:"strategy_manifest_sha256"`
	SourceConfigSHA256     string `json:"source_config_sha256"`
	RouteSHA256            string `json:"route_sha256"`
	OraclePolicySHA256     string `json:"oracle_policy_sha256"`
	RiskPolicySHA256       string `json:"risk_policy_sha256"`
	ChainID                uint64 `json:"chain_id"`
	Symbol                 string `json:"symbol"`
	SettlementToken        string `json:"settlement_token"`
	SpotToken              string `json:"spot_token"`
	Router                 string `json:"router"`
	Permit2                string `json:"permit2"`
	PoolManager            string `json:"pool_manager"`
	PoolID                 string `json:"pool_id"`
	PoolFee                uint32 `json:"pool_fee"`
	PoolTickSpacing        int32  `json:"pool_tick_spacing"`
	PoolHooks              string `json:"pool_hooks"`
	LighterEndpoint        string `json:"lighter_endpoint"`
	LighterMarketIndex     uint32 `json:"lighter_market_index"`
	LighterBaseAssetID     uint32 `json:"lighter_base_asset_id"`
	LighterQuoteAssetID    uint32 `json:"lighter_quote_asset_id"`
	PerpBaseDecimals       uint8  `json:"perp_base_decimals"`
	PerpPriceDecimals      uint8  `json:"perp_price_decimals"`
	PerpQuoteDecimals      uint8  `json:"perp_quote_decimals"`
	SpotDecimals           uint8  `json:"spot_decimals"`
	SpotConfigVersion      uint64 `json:"spot_config_version"`
	UIMultiplierE18        string `json:"ui_multiplier_e18"`
	MaxPriceDeviationBPS   uint16 `json:"max_price_deviation_bps"`
	MaxSpotSlippageBPS     uint16 `json:"max_spot_slippage_bps"`
	MaxUnwindDeviationBPS  uint16 `json:"max_unwind_price_deviation_bps"`
	ValidFromMS            int64  `json:"valid_from_ms"`
	ValidUntilMS           int64  `json:"valid_until_ms"`
}

type LighterMarketSource struct {
	endpoint *url.URL
	client   *http.Client
	now      func() time.Time
}

func NewLighterMarketSource(client *http.Client, now func() time.Time) *LighterMarketSource {
	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("Lighter metadata redirects are not allowed")
			},
		}
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	endpoint, _ := url.Parse(lighterMainnetEndpoint)
	return &LighterMarketSource{endpoint: endpoint, client: client, now: now}
}

func (source *LighterMarketSource) Discover(ctx context.Context, expected MarketBootstrapConfig) (LighterMarketMetadata, error) {
	endpoint := *source.endpoint
	endpoint.Path = "/api/v1/orderBooks"
	endpoint.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return LighterMarketMetadata{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-cache, no-store")
	response, err := source.client.Do(request)
	if err != nil {
		return LighterMarketMetadata{}, fmt.Errorf("fetch official Lighter market metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return LighterMarketMetadata{}, fmt.Errorf("Lighter market metadata returned HTTP %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return LighterMarketMetadata{}, errors.New("Lighter market metadata is not JSON")
	}
	if age := strings.TrimSpace(response.Header.Get("Age")); age != "" && age != "0" {
		return LighterMarketMetadata{}, errors.New("Lighter market metadata came from a cache")
	}
	now := source.now().UTC()
	serverDate, err := http.ParseTime(response.Header.Get("Date"))
	if err != nil || serverDate.Before(now.Add(-maximumMetadataAge)) || serverDate.After(now.Add(maximumMetadataAge)) {
		return LighterMarketMetadata{}, errors.New("Lighter market metadata has no fresh server time")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumMetadataBytes+1))
	if err != nil || len(body) == 0 || len(body) > maximumMetadataBytes {
		return LighterMarketMetadata{}, errors.New("Lighter market metadata body is invalid")
	}
	metadata, err := parseLighterMarkets(body, expected)
	if err != nil {
		return LighterMarketMetadata{}, err
	}
	digest := sha256.Sum256(body)
	metadata.ResponseSHA256 = hex.EncodeToString(digest[:])
	metadata.ObservedAt = now
	return metadata, nil
}

func parseLighterMarkets(body []byte, expected MarketBootstrapConfig) (LighterMarketMetadata, error) {
	var envelope struct {
		Code       *int              `json:"code"`
		OrderBooks []json.RawMessage `json:"order_books"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		envelope.Code == nil || *envelope.Code != http.StatusOK || len(envelope.OrderBooks) == 0 {
		return LighterMarketMetadata{}, errors.New("Lighter orderBooks response is invalid")
	}
	var matches []LighterMarketMetadata
	for _, raw := range envelope.OrderBooks {
		var market struct {
			Symbol        *string `json:"symbol"`
			MarketID      *uint32 `json:"market_id"`
			MarketType    *string `json:"market_type"`
			BaseAssetID   *uint32 `json:"base_asset_id"`
			QuoteAssetID  *uint32 `json:"quote_asset_id"`
			Status        *string `json:"status"`
			SizeDecimals  *uint8  `json:"supported_size_decimals"`
			PriceDecimals *uint8  `json:"supported_price_decimals"`
			QuoteDecimals *uint8  `json:"supported_quote_decimals"`
		}
		if err := json.Unmarshal(raw, &market); err != nil {
			return LighterMarketMetadata{}, errors.New("Lighter orderBooks contains malformed metadata")
		}
		if market.Symbol == nil || market.MarketType == nil || *market.Symbol != Symbol || *market.MarketType != "perp" {
			continue
		}
		if market.Status == nil {
			return LighterMarketMetadata{}, errors.New("Lighter AAPL perpetual metadata is incomplete")
		}
		if *market.Status != "active" {
			continue
		}
		if market.MarketID == nil || market.BaseAssetID == nil || market.QuoteAssetID == nil ||
			market.SizeDecimals == nil || market.PriceDecimals == nil || market.QuoteDecimals == nil {
			return LighterMarketMetadata{}, errors.New("Lighter AAPL perpetual metadata is incomplete")
		}
		matches = append(matches, LighterMarketMetadata{
			Symbol: *market.Symbol, MarketIndex: *market.MarketID, MarketType: *market.MarketType,
			Status: *market.Status, BaseAssetID: *market.BaseAssetID, QuoteAssetID: *market.QuoteAssetID,
			BaseDecimals: *market.SizeDecimals, PriceDecimals: *market.PriceDecimals,
			QuoteDecimals: *market.QuoteDecimals,
		})
	}
	if len(matches) != 1 {
		return LighterMarketMetadata{}, errors.New("exactly one Lighter AAPL perpetual is required")
	}
	market := matches[0]
	if market.Status != "active" || market.MarketIndex != expected.ExpectedMarketIndex ||
		market.BaseDecimals != expected.ExpectedBaseDecimals || market.PriceDecimals != expected.ExpectedPriceDecimals {
		return LighterMarketMetadata{}, errors.New("Lighter AAPL perpetual identity does not match the pinned release")
	}
	return market, nil
}

func BootstrapMarketConfig(ctx context.Context, source MarketMetadataSource, writer MarketConfigWriter,
	config MarketBootstrapConfig, now time.Time) (MarketConfig, bool, error) {
	if source == nil || writer == nil {
		return MarketConfig{}, false, errors.New("market metadata source and writer are required")
	}
	if err := validateMarketBootstrap(config, now); err != nil {
		return MarketConfig{}, false, err
	}
	metadata, err := source.Discover(ctx, config)
	if err != nil {
		return MarketConfig{}, false, err
	}
	record, err := buildMarketConfigRecord(config, metadata)
	if err != nil {
		return MarketConfig{}, false, err
	}
	inserted, err := writer.EnsureMarketConfig(ctx, record)
	return record.Config, inserted, err
}

func validateMarketBootstrap(config MarketBootstrapConfig, now time.Time) error {
	if config.ExpectedMarketIndex == 0 || config.ExpectedMarketIndex > 32_767 ||
		config.ExpectedBaseDecimals > 18 || config.ExpectedPriceDecimals > 18 ||
		config.SpotConfigVersion == 0 || !positiveInteger(config.UIMultiplierE18) || len(config.UIMultiplierE18) > 39 ||
		config.MaxPriceDeviationBPS == 0 || config.MaxPriceDeviationBPS > 500 ||
		config.MaxUnwindPriceDeviationBPS == 0 || config.MaxUnwindPriceDeviationBPS > 5_000 ||
		!config.ValidUntil.After(config.ValidFrom) || now.Before(config.ValidFrom) || !now.Before(config.ValidUntil) {
		return errors.New("market bootstrap policy is invalid or outside its release window")
	}
	return nil
}

func buildMarketConfigRecord(config MarketBootstrapConfig, metadata LighterMarketMetadata) (MarketConfigRecord, error) {
	responseDigest, digestErr := hex.DecodeString(metadata.ResponseSHA256)
	if metadata.Symbol != Symbol || metadata.MarketType != "perp" || metadata.Status != "active" ||
		metadata.MarketIndex != config.ExpectedMarketIndex || metadata.BaseDecimals != config.ExpectedBaseDecimals ||
		metadata.PriceDecimals != config.ExpectedPriceDecimals || metadata.QuoteDecimals > 38 ||
		digestErr != nil || len(responseDigest) != sha256.Size || metadata.ObservedAt.IsZero() {
		return MarketConfigRecord{}, errors.New("Lighter metadata evidence does not match the pinned release")
	}
	review := MarketReview{
		SchemaVersion: marketSpotVersion, StrategyVersion: scheduler.StrategyVersion,
		StrategyManifestSHA256: scheduler.StrategyManifestSHA256, SourceConfigSHA256: scheduler.SourceConfigSHA256,
		RouteSHA256: scheduler.RouteSHA256, OraclePolicySHA256: scheduler.OraclePolicySHA256,
		RiskPolicySHA256: scheduler.RiskPolicySHA256, ChainID: marketChainID, Symbol: Symbol,
		SettlementToken: marketSettlementToken, SpotToken: stockToken, Router: marketRouter, Permit2: marketPermit2,
		PoolManager: marketPoolManager, PoolID: marketPoolID, PoolFee: marketPoolFee,
		PoolTickSpacing: marketPoolTickSpacing, PoolHooks: marketHooks, LighterEndpoint: lighterMainnetEndpoint,
		LighterMarketIndex: metadata.MarketIndex, LighterBaseAssetID: metadata.BaseAssetID,
		LighterQuoteAssetID: metadata.QuoteAssetID, PerpBaseDecimals: metadata.BaseDecimals,
		PerpPriceDecimals: metadata.PriceDecimals, PerpQuoteDecimals: metadata.QuoteDecimals,
		SpotDecimals: marketSpotDecimals, SpotConfigVersion: config.SpotConfigVersion,
		UIMultiplierE18: config.UIMultiplierE18, MaxPriceDeviationBPS: config.MaxPriceDeviationBPS,
		MaxSpotSlippageBPS: marketSpotSlippageBPS, MaxUnwindDeviationBPS: config.MaxUnwindPriceDeviationBPS,
		ValidFromMS: config.ValidFrom.UnixMilli(), ValidUntilMS: config.ValidUntil.UnixMilli(),
	}
	reviewDigest, err := domainHash("robin.live.market-review.v1", review)
	if err != nil {
		return MarketConfigRecord{}, err
	}
	market := MarketConfig{
		Symbol: Symbol, SpotToken: stockToken, LighterMarketIndex: metadata.MarketIndex,
		SpotDecimals: marketSpotDecimals, PerpBaseDecimals: metadata.BaseDecimals,
		PerpPriceDecimals: metadata.PriceDecimals, SpotConfigVersion: config.SpotConfigVersion,
		UIMultiplierE18: config.UIMultiplierE18, MaxPriceDeviationBPS: config.MaxPriceDeviationBPS,
		MaxSpotSlippageBPS: marketSpotSlippageBPS, MaxUnwindPriceDeviationBPS: config.MaxUnwindPriceDeviationBPS,
		ReviewRecordSHA256: strings.TrimPrefix(reviewDigest, "0x"), ValidFrom: config.ValidFrom, ValidUntil: config.ValidUntil,
	}
	market.ManifestID, err = MarketManifest(market)
	if err != nil {
		return MarketConfigRecord{}, err
	}
	return MarketConfigRecord{Config: market, Review: review, MetadataSHA256: metadata.ResponseSHA256,
		MetadataObservedAt: metadata.ObservedAt}, nil
}

func (store *PGStore) EnsureMarketConfig(ctx context.Context, record MarketConfigRecord) (bool, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", marketBootstrapLock); err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx, `
SELECT manifest_id, symbol, lower(spot_token), lighter_market_index, spot_decimals,
       perp_base_decimals, perp_price_decimals, spot_config_version, ui_multiplier_e18,
       max_price_deviation_bps, max_spot_slippage_bps, max_unwind_price_deviation_bps,
       review_record_sha256, valid_from, valid_until
FROM execution_market_configs
WHERE symbol = $1 AND valid_from < $2 AND valid_until > $3
ORDER BY manifest_id
FOR UPDATE`, Symbol, record.Config.ValidUntil, record.Config.ValidFrom)
	if err != nil {
		return false, err
	}
	var overlaps []MarketConfig
	for rows.Next() {
		config, scanErr := scanMarketConfig(rows)
		if scanErr != nil {
			rows.Close()
			return false, scanErr
		}
		overlaps = append(overlaps, config)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(overlaps) > 1 || (len(overlaps) == 1 && !sameMarketConfig(overlaps[0], record.Config)) {
		return false, errors.New("an overlapping AAPL market release already exists")
	}
	reviewJSON, err := json.Marshal(record.Review)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO execution_market_review_records (review_record_sha256, record)
VALUES ($1, $2::jsonb)
ON CONFLICT (review_record_sha256) DO NOTHING`, record.Config.ReviewRecordSHA256, reviewJSON); err != nil {
		return false, err
	}
	var reviewMatches bool
	if err := tx.QueryRow(ctx, `
SELECT record = $2::jsonb
FROM execution_market_review_records
WHERE review_record_sha256 = $1`, record.Config.ReviewRecordSHA256, reviewJSON).Scan(&reviewMatches); err != nil || !reviewMatches {
		return false, errors.New("stored market review record does not match its digest")
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO execution_market_review_observations
    (review_record_sha256, source, response_sha256, observed_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (review_record_sha256, response_sha256) DO NOTHING`, record.Config.ReviewRecordSHA256,
		lighterMainnetEndpoint+"/api/v1/orderBooks", record.MetadataSHA256, record.MetadataObservedAt); err != nil {
		return false, err
	}
	inserted := len(overlaps) == 0
	if inserted {
		config := record.Config
		if _, err := tx.Exec(ctx, `
INSERT INTO execution_market_configs
    (manifest_id, symbol, spot_token, lighter_market_index, spot_decimals,
     perp_base_decimals, perp_price_decimals, spot_config_version, ui_multiplier_e18,
     max_price_deviation_bps, max_spot_slippage_bps, max_unwind_price_deviation_bps,
     review_record_sha256, valid_from, valid_until)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
			config.ManifestID, config.Symbol, config.SpotToken, config.LighterMarketIndex,
			config.SpotDecimals, config.PerpBaseDecimals, config.PerpPriceDecimals,
			config.SpotConfigVersion, config.UIMultiplierE18, config.MaxPriceDeviationBPS,
			config.MaxSpotSlippageBPS, config.MaxUnwindPriceDeviationBPS,
			config.ReviewRecordSHA256, config.ValidFrom, config.ValidUntil); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return inserted, nil
}

type marketConfigScanner interface {
	Scan(...any) error
}

func scanMarketConfig(row marketConfigScanner) (MarketConfig, error) {
	var config MarketConfig
	var market, spotDecimals, baseDecimals, priceDecimals int32
	var spotVersion int64
	var priceDeviation, spotSlippage, unwindDeviation int32
	if err := row.Scan(&config.ManifestID, &config.Symbol, &config.SpotToken, &market,
		&spotDecimals, &baseDecimals, &priceDecimals, &spotVersion, &config.UIMultiplierE18,
		&priceDeviation, &spotSlippage, &unwindDeviation, &config.ReviewRecordSHA256,
		&config.ValidFrom, &config.ValidUntil); err != nil {
		return MarketConfig{}, err
	}
	if market < 0 || spotDecimals < 0 || baseDecimals < 0 || priceDecimals < 0 || spotVersion <= 0 ||
		priceDeviation <= 0 || spotSlippage <= 0 || unwindDeviation <= 0 {
		return MarketConfig{}, errors.New("stored market config contains an invalid value")
	}
	config.LighterMarketIndex = uint32(market)
	config.SpotDecimals = uint8(spotDecimals)
	config.PerpBaseDecimals = uint8(baseDecimals)
	config.PerpPriceDecimals = uint8(priceDecimals)
	config.SpotConfigVersion = uint64(spotVersion)
	config.MaxPriceDeviationBPS = uint16(priceDeviation)
	config.MaxSpotSlippageBPS = uint16(spotSlippage)
	config.MaxUnwindPriceDeviationBPS = uint16(unwindDeviation)
	return config, nil
}

func sameMarketConfig(left, right MarketConfig) bool {
	return left.ManifestID == right.ManifestID && left.Symbol == right.Symbol && left.SpotToken == right.SpotToken &&
		left.LighterMarketIndex == right.LighterMarketIndex && left.SpotDecimals == right.SpotDecimals &&
		left.PerpBaseDecimals == right.PerpBaseDecimals && left.PerpPriceDecimals == right.PerpPriceDecimals &&
		left.SpotConfigVersion == right.SpotConfigVersion && left.UIMultiplierE18 == right.UIMultiplierE18 &&
		left.MaxPriceDeviationBPS == right.MaxPriceDeviationBPS && left.MaxSpotSlippageBPS == right.MaxSpotSlippageBPS &&
		left.MaxUnwindPriceDeviationBPS == right.MaxUnwindPriceDeviationBPS &&
		left.ReviewRecordSHA256 == right.ReviewRecordSHA256 && left.ValidFrom.Equal(right.ValidFrom) &&
		left.ValidUntil.Equal(right.ValidUntil)
}
