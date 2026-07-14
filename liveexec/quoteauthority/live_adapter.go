package quoteauthority

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

const (
	liveAdapterID             = "robin-mainnet-aapl-executable-v1"
	durableSourceSession      = "quote-authority-aapl-v1"
	maximumAdapterDuration    = 4500 * time.Millisecond
	maximumSourceAge          = 5 * time.Second
	maximumSourceSkew         = 5 * time.Second
	spotSlippageBPS           = uint64(100)
	quoterAddress             = "0x8dc178efb8111bb0973dd9d722ebeff267c98f94"
	quoterCodeHash            = "0xd707b1da8cb165e5ea35a3b4450d971eb562ec171e23492aa117036b78a868f6"
	poolManagerAddress        = "0x8366a39cc670b4001a1121b8f6a443a643e40951"
	poolManagerCodeHash       = "0xbd3881180b547f5fe817545743cfb4343e96b1bc6640dcd70c106b0066e95626"
	settlementCodeHash        = "0x864cc9ad53b338b82da1f7cab85ab0b3d5c8861acb422b6fec63cf36234f36a6"
	stockCodeHash             = "0x6c1fdd40002dcb440c7fff6a84171404d279ccb057803b65826f7546acd65630"
	settlementDecimals        = uint8(6)
	stockDecimals             = uint8(18)
	poolFee                   = uint32(10_000)
	poolTickSpacing           = int32(200)
	poolID                    = "0xda4116b5894ee7479e64eae9276e1a2944ef0e5ce863a299d296a15618deee01"
	zeroAddress               = "0x0000000000000000000000000000000000000000"
	officialLighterAPIHost    = "mainnet.zklighter.elliot.ai"
	maximumLighterResponse    = 2 << 20
	maximumRPCResponse        = 4 << 20
	maximumEntrySizingRetries = 6
)

type LiveAdapterConfig struct {
	PrimaryRPCURL          string
	SecondaryRPCURL        string
	LighterAPIURL          string
	ReferenceFeed          string
	ReferenceFeedCodeHash  string
	ReferenceFeedDecimals  uint8
	ReferenceFeedHeartbeat time.Duration
	LighterMarketIndex     uint32
	LighterBaseDecimals    uint8
	LighterPriceDecimals   uint8
}

type OpenEpisode struct {
	SchemaVersion      uint8  `json:"schema_version"`
	ExecutionAccountID string `json:"execution_account_id"`
	IntentID           string `json:"intent_id"`
	Phase              string `json:"phase"`
	SpotAmount         string `json:"spot_amount"`
	PerpBaseAmount     uint64 `json:"perp_base_amount"`
	ObservedAtMS       uint64 `json:"observed_at_ms"`
}

type OpenEpisodeResolver interface {
	Resolve(context.Context, string, string) (OpenEpisode, error)
}

type LiveAdapter struct {
	chain    *chainReader
	lighter  *lighterReader
	resolver OpenEpisodeResolver
	now      func() time.Time
}

func NewLiveAdapter(config LiveAdapterConfig, resolver OpenEpisodeResolver) (*LiveAdapter, error) {
	client := secureHTTPClient(maximumAdapterDuration)
	return newLiveAdapter(config, resolver, client, time.Now)
}

func newLiveAdapter(config LiveAdapterConfig, resolver OpenEpisodeResolver, client *http.Client, now func() time.Time) (*LiveAdapter, error) {
	if client == nil || now == nil {
		return nil, errors.New("live quote adapter requires an HTTP client and clock")
	}
	if err := config.validate(false); err != nil {
		return nil, err
	}
	chain, err := newChainReader(config, client, now)
	if err != nil {
		return nil, err
	}
	lighter, err := newLighterReader(config, client, now)
	if err != nil {
		return nil, err
	}
	return &LiveAdapter{chain: chain, lighter: lighter, resolver: resolver, now: now}, nil
}

func (c LiveAdapterConfig) validate(production bool) error {
	computedPoolID, err := canonicalPoolID()
	if err != nil || computedPoolID != poolID {
		return errors.New("canonical AAPL pool identity is invalid")
	}
	primary, err := endpointURL(c.PrimaryRPCURL, false)
	if err != nil {
		return fmt.Errorf("primary Robinhood RPC: %w", err)
	}
	secondary, err := endpointURL(c.SecondaryRPCURL, false)
	if err != nil {
		return fmt.Errorf("secondary Robinhood RPC: %w", err)
	}
	if primary.String() == secondary.String() || primary.Host == secondary.Host {
		return errors.New("Robinhood RPC origins must be independent")
	}
	lighter, err := endpointURL(c.LighterAPIURL, true)
	if err != nil {
		return fmt.Errorf("Lighter API: %w", err)
	}
	if production && (lighter.Scheme != "https" || !strings.EqualFold(lighter.Hostname(), officialLighterAPIHost)) {
		return errors.New("Lighter API must be the official mainnet HTTPS origin")
	}
	if !validAddress(c.ReferenceFeed) || !validCodeHash(c.ReferenceFeedCodeHash) {
		return errors.New("reviewed AAPL reference feed address and code hash are required")
	}
	if c.ReferenceFeedDecimals != 8 || c.ReferenceFeedHeartbeat != 25*time.Hour {
		return errors.New("AAPL reference feed decimals or heartbeat are invalid")
	}
	if c.LighterMarketIndex > 32767 || c.LighterBaseDecimals > 18 || c.LighterPriceDecimals > 18 {
		return errors.New("reviewed Lighter market identity is invalid")
	}
	return nil
}

func (a *LiveAdapter) Quote(ctx context.Context, request AdapterRequest) (AdapterResult, error) {
	if !validHash(request.RequestID) || !validExecutionID(request.ExecutionAccountID) ||
		(request.Action != protocol.ActionEntry && request.Action != protocol.ActionUnwind) {
		return AdapterResult{}, errors.New("invalid live adapter request")
	}
	ctx, cancel := context.WithTimeout(ctx, maximumAdapterDuration)
	defer cancel()

	var episode OpenEpisode
	var err error
	input := new(big.Int).SetUint64(request.EntryNotional)
	if request.Action == protocol.ActionUnwind {
		if a.resolver == nil {
			return AdapterResult{}, errors.New("open episode resolver is unavailable")
		}
		episode, err = a.resolver.Resolve(ctx, request.ExecutionAccountID, request.IntentID)
		if err != nil {
			return AdapterResult{}, fmt.Errorf("resolve open episode: %w", err)
		}
		if err := validateEpisode(episode, request, uint64(a.now().UnixMilli())); err != nil {
			return AdapterResult{}, err
		}
		input, _ = new(big.Int).SetString(episode.SpotAmount, 10)
	}
	if input == nil || input.Sign() <= 0 || input.BitLen() > 128 {
		return AdapterResult{}, errors.New("quote input amount is invalid")
	}

	type chainResult struct {
		value chainSnapshot
		err   error
	}
	type lighterResult struct {
		value lighterSnapshot
		err   error
	}
	chainCh := make(chan chainResult, 1)
	lighterCh := make(chan lighterResult, 1)
	go func() {
		value, err := a.chain.snapshot(ctx, request.Action, input)
		chainCh <- chainResult{value, err}
	}()
	go func() {
		value, err := a.lighter.snapshot(ctx)
		lighterCh <- lighterResult{value, err}
	}()
	chainSource := <-chainCh
	lighterSource := <-lighterCh
	if chainSource.err != nil {
		return AdapterResult{}, chainSource.err
	}
	if lighterSource.err != nil {
		return AdapterResult{}, lighterSource.err
	}
	chain := chainSource.value
	lighter := lighterSource.value

	stockExposure := chain.SpotAmountOut
	if request.Action == protocol.ActionUnwind {
		stockExposure = chain.InputAmount
	}
	baseAmount, err := underlyingBase(stockExposure, chain.UIMultiplier, stockDecimals, lighter.BaseDecimals)
	if err != nil {
		return AdapterResult{}, err
	}
	phase := ""
	var limitPrice uint32
	if request.Action == protocol.ActionEntry {
		if request.EntryNotional == 0 || request.EntryNotional > protocol.EntryNotionalMicros {
			return AdapterResult{}, errors.New("entry notional cap is invalid")
		}
		chain, baseAmount, limitPrice, err = a.fitEntry(ctx, chain, lighter, request.EntryNotional, baseAmount)
		if err != nil {
			return AdapterResult{}, err
		}
	} else {
		phase = episode.Phase
		if phase == "perp_and_spot" && baseAmount != episode.PerpBaseAmount {
			return AdapterResult{}, errors.New("open episode spot and perp quantities disagree")
		}
		if phase == "spot_only" && episode.PerpBaseAmount != 0 {
			return AdapterResult{}, errors.New("spot-only episode retains perp exposure")
		}
		limitPrice, err = lighter.executablePrice("ask", episode.PerpBaseAmount)
		if err != nil {
			return AdapterResult{}, err
		}
		baseAmount = episode.PerpBaseAmount
	}

	nowMS := uint64(a.now().UnixMilli())
	if err := validateSourceTimes(chain.ObservedAtMS, lighter.ObservedAtMS, nowMS); err != nil {
		return AdapterResult{}, err
	}
	observedAt := maxUint64(chain.ObservedAtMS, lighter.ObservedAtMS)
	expiresAt := observedAt + uint64(maximumSourceAge/time.Millisecond)
	if expiresAt <= nowMS {
		return AdapterResult{}, errors.New("quote sources expired during assembly")
	}
	minimum := minimumOutput(chain.SpotAmountOut, spotSlippageBPS)
	settlementAmount := chain.InputAmount.String()
	stockAmount := chain.SpotAmountOut.String()
	if request.Action == protocol.ActionUnwind {
		settlementAmount = chain.SpotAmountOut.String()
		stockAmount = chain.InputAmount.String()
	}
	result := AdapterResult{
		Source: protocol.SourceIdentity{
			AdapterID:   liveAdapterID,
			SpotSource:  "robinhood-dual-canonical:" + chain.BlockHash,
			PerpSource:  "lighter-mainnet-rest-tls:" + lighter.PayloadSHA256,
			OracleRound: chain.OracleRound.String(),
		},
		Spot: protocol.SpotQuote{
			Venue: protocol.SpotVenue, ChainID: protocol.ChainID, SettlementToken: protocol.SettlementToken,
			StockToken: protocol.StockToken, Router: protocol.Router,
			Side:             map[bool]string{true: "buy", false: "sell"}[request.Action == protocol.ActionEntry],
			SettlementAmount: settlementAmount, StockAmount: stockAmount, MinimumAmountOut: minimum.String(),
			ReferencePriceMicros: chain.ReferencePriceMicros, BlockHash: chain.BlockHash, ObservedAtMS: observedAt,
			ExpectedUIMultiplier: chain.UIMultiplier.String(),
			MinOracleRoundID:     chain.OracleRound.String(),
		},
		Perp: protocol.PerpQuote{
			Venue: protocol.PerpVenue, Symbol: protocol.Symbol, MarketIndex: lighter.MarketIndex,
			Side:       map[bool]string{true: "short", false: "long"}[request.Action == protocol.ActionEntry],
			ReduceOnly: request.Action == protocol.ActionUnwind, Phase: phase, BaseAmount: baseAmount,
			BaseDecimals: lighter.BaseDecimals, PriceDecimals: lighter.PriceDecimals,
			LimitPrice: limitPrice, MarkPrice: lighter.MarkPrice, ObservedAtMS: observedAt,
		},
		DurableSource: durableIdentity(request.RequestID, observedAt),
		ObservedAtMS:  observedAt,
		ExpiresAtMS:   expiresAt,
	}
	return result, nil
}

func (a *LiveAdapter) fitEntry(ctx context.Context, chain chainSnapshot, lighter lighterSnapshot, cap uint64, base uint64) (chainSnapshot, uint64, uint32, error) {
	if base == 0 {
		return chainSnapshot{}, 0, 0, errors.New("spot quote has zero underlying exposure")
	}
	for attempt := 0; attempt <= maximumEntrySizingRetries; attempt++ {
		limit, err := lighter.executablePrice("bid", base)
		if err != nil {
			return chainSnapshot{}, 0, 0, err
		}
		notional := quoteNotional(base, lighter.BaseDecimals, maxUint32(limit, lighter.MarkPrice), lighter.PriceDecimals)
		if notional != nil && notional.Cmp(new(big.Int).SetUint64(cap)) <= 0 {
			return chain, base, limit, nil
		}
		if attempt == maximumEntrySizingRetries || notional == nil || notional.Sign() <= 0 {
			break
		}
		next := new(big.Int).Mul(chain.InputAmount, new(big.Int).SetUint64(cap))
		next.Div(next, notional)
		if next.Cmp(chain.InputAmount) >= 0 {
			next.Sub(chain.InputAmount, big.NewInt(1))
		}
		if next.Sign() <= 0 {
			break
		}
		chain, err = a.chain.requote(ctx, chain, next)
		if err != nil {
			return chainSnapshot{}, 0, 0, err
		}
		base, err = underlyingBase(chain.SpotAmountOut, chain.UIMultiplier, stockDecimals, lighter.BaseDecimals)
		if err != nil || base == 0 {
			break
		}
	}
	return chainSnapshot{}, 0, 0, errors.New("no matched entry size fits the canary caps")
}

func validateEpisode(episode OpenEpisode, request AdapterRequest, nowMS uint64) error {
	spot, ok := positiveDecimal(episode.SpotAmount)
	if episode.SchemaVersion != 1 || episode.ExecutionAccountID != request.ExecutionAccountID || episode.IntentID != request.IntentID ||
		!ok || spot.BitLen() > 128 || episode.ObservedAtMS > nowMS || nowMS-episode.ObservedAtMS > uint64(maximumSourceAge/time.Millisecond) {
		return errors.New("open episode identity or state is invalid")
	}
	if (episode.Phase == "perp_and_spot" && episode.PerpBaseAmount == 0) ||
		(episode.Phase == "spot_only" && episode.PerpBaseAmount != 0) ||
		(episode.Phase != "perp_and_spot" && episode.Phase != "spot_only") {
		return errors.New("open episode phase is invalid")
	}
	return nil
}

func validateSourceTimes(chainMS, lighterMS, nowMS uint64) error {
	for _, observed := range []uint64{chainMS, lighterMS} {
		if observed > nowMS || nowMS-observed > uint64(maximumSourceAge/time.Millisecond) {
			return errors.New("quote source is stale")
		}
	}
	if absDiff(chainMS, lighterMS) > uint64(maximumSourceSkew/time.Millisecond) {
		return errors.New("quote sources are not simultaneous")
	}
	return nil
}

func underlyingBase(stockRaw, multiplier *big.Int, spotDecimals, baseDecimals uint8) (uint64, error) {
	if stockRaw == nil || multiplier == nil || stockRaw.Sign() <= 0 || multiplier.Sign() <= 0 {
		return 0, errors.New("spot exposure is invalid")
	}
	value := new(big.Int).Mul(stockRaw, multiplier)
	value.Mul(value, pow10(baseDecimals))
	value.Div(value, pow10(18+spotDecimals))
	if !value.IsUint64() || value.Sign() <= 0 {
		return 0, errors.New("spot exposure cannot be represented on Lighter")
	}
	return value.Uint64(), nil
}

func quoteNotional(base uint64, baseDecimals uint8, price uint32, priceDecimals uint8) *big.Int {
	if base == 0 || price == 0 {
		return nil
	}
	numerator := new(big.Int).SetUint64(base)
	numerator.Mul(numerator, new(big.Int).SetUint64(uint64(price)))
	numerator.Mul(numerator, big.NewInt(1_000_000))
	denominator := pow10(baseDecimals + priceDecimals)
	numerator.Add(numerator, new(big.Int).Sub(denominator, big.NewInt(1)))
	return numerator.Div(numerator, denominator)
}

func minimumOutput(amount *big.Int, slippageBPS uint64) *big.Int {
	retained := uint64(10_000) - slippageBPS
	value := new(big.Int).Mul(amount, new(big.Int).SetUint64(retained))
	value.Add(value, big.NewInt(9_999))
	return value.Div(value, big.NewInt(10_000))
}

func durableIdentity(requestID string, observedAtMS uint64) DurableSource {
	digest := sha256.Sum256([]byte(requestID))
	sequence := binary.BigEndian.Uint64(digest[:8]) & math.MaxInt64
	return DurableSource{
		Session: durableSourceSession, EventID: requestID, Sequence: int64(sequence), ReceivedAtMS: observedAtMS,
	}
}

func endpointURL(raw string, allowRootOnly bool) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("endpoint URL is invalid")
	}
	if allowRootOnly && parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("endpoint URL must be an origin")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && privateHost(parsed.Hostname())) {
		return nil, errors.New("endpoint must use HTTPS or private-network HTTP")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}

func validAddress(value string) bool {
	if len(value) != 42 || !strings.HasPrefix(value, "0x") || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil && value != zeroAddress
}

func validCodeHash(value string) bool {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") || value != strings.ToLower(value) || value == "0x"+strings.Repeat("0", 64) {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}

func pow10(decimals uint8) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), new(big.Int).SetUint64(uint64(decimals)), nil)
}

func absDiff(left, right uint64) uint64 {
	if left > right {
		return left - right
	}
	return right - left
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}

func maxUint32(left, right uint32) uint32 {
	if left > right {
		return left
	}
	return right
}
