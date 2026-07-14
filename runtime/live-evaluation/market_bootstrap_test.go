package evaluation

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	scheduler "github.com/robin-the-claw/live-scheduler"
)

var marketTestTime = time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

func marketBootstrapFixture() MarketBootstrapConfig {
	return MarketBootstrapConfig{
		ExpectedMarketIndex: 101, ExpectedBaseDecimals: 4, ExpectedPriceDecimals: 3,
		SpotConfigVersion: 1, UIMultiplierE18: "1000000000000000000",
		MaxPriceDeviationBPS: 100, MaxUnwindPriceDeviationBPS: 2_500,
		ValidFrom: marketTestTime.Add(-time.Hour), ValidUntil: marketTestTime.Add(time.Hour),
	}
}

func lighterMarketJSON(extra string) string {
	market := `{
        "symbol":"AAPL","market_id":101,"market_type":"perp","base_asset_id":44,
        "quote_asset_id":1,"status":"active","supported_size_decimals":4,
        "supported_price_decimals":3,"supported_quote_decimals":7
    }`
	if extra != "" {
		market += "," + extra
	}
	return `{"code":200,"order_books":[` + market + `]}`
}

func TestParseLighterMarketsRequiresOnePinnedAAPLPerpetual(t *testing.T) {
	config := marketBootstrapFixture()
	market, err := parseLighterMarkets([]byte(lighterMarketJSON("")), config)
	if err != nil {
		t.Fatal(err)
	}
	if market.MarketIndex != 101 || market.BaseDecimals != 4 || market.PriceDecimals != 3 || market.Status != "active" {
		t.Fatalf("unexpected market: %+v", market)
	}
	inactive := `{
        "symbol":"AAPL","market_id":99,"market_type":"perp","status":"inactive"
    }`
	if _, err := parseLighterMarkets([]byte(lighterMarketJSON(inactive)), config); err != nil {
		t.Fatalf("inactive historical AAPL market blocked the active release: %v", err)
	}
	duplicate := `{
        "symbol":"AAPL","market_id":102,"market_type":"perp","base_asset_id":45,
        "quote_asset_id":1,"status":"active","supported_size_decimals":4,
        "supported_price_decimals":3,"supported_quote_decimals":7
    }`
	if _, err := parseLighterMarkets([]byte(lighterMarketJSON(duplicate)), config); err == nil ||
		!strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("duplicate AAPL perpetual was accepted: %v", err)
	}
	config.ExpectedMarketIndex = 102
	if _, err := parseLighterMarkets([]byte(lighterMarketJSON("")), config); err == nil ||
		!strings.Contains(err.Error(), "pinned release") {
		t.Fatalf("market substitution was accepted: %v", err)
	}
}

func TestLighterMarketSourceUsesOfficialResponseShapeAndFreshness(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/orderBooks" ||
			request.Header.Get("Cache-Control") != "no-cache, no-store" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}, "Date": {marketTestTime.Format(http.TimeFormat)}},
			Body:       io.NopCloser(strings.NewReader(lighterMarketJSON(""))),
		}, nil
	})}
	source := NewLighterMarketSource(client, func() time.Time { return marketTestTime })
	market, err := source.Discover(context.Background(), marketBootstrapFixture())
	if err != nil {
		t.Fatal(err)
	}
	if len(market.ResponseSHA256) != 64 || !market.ObservedAt.Equal(marketTestTime) {
		t.Fatalf("metadata evidence was not retained: %+v", market)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type staticMarketSource struct{ market LighterMarketMetadata }

func (source staticMarketSource) Discover(context.Context, MarketBootstrapConfig) (LighterMarketMetadata, error) {
	return source.market, nil
}

type memoryMarketWriter struct {
	mu     sync.Mutex
	record *MarketConfigRecord
}

func (writer *memoryMarketWriter) EnsureMarketConfig(_ context.Context, record MarketConfigRecord) (bool, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.record == nil {
		writer.record = &record
		return true, nil
	}
	if !sameMarketConfig(writer.record.Config, record.Config) || writer.record.Review != record.Review {
		return false, fmt.Errorf("overlapping market release")
	}
	return false, nil
}

func TestBootstrapMarketConfigIsIdempotentUnderConcurrency(t *testing.T) {
	config := marketBootstrapFixture()
	source := staticMarketSource{LighterMarketMetadata{
		Symbol: Symbol, MarketIndex: 101, MarketType: "perp", Status: "active",
		BaseAssetID: 44, QuoteAssetID: 1, BaseDecimals: 4, PriceDecimals: 3, QuoteDecimals: 7,
		ResponseSHA256: strings.Repeat("a", 64), ObservedAt: marketTestTime,
	}}
	writer := &memoryMarketWriter{}
	var inserted atomic.Int32
	errorsFound := make(chan error, 32)
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			market, created, err := BootstrapMarketConfig(context.Background(), source, writer, config, marketTestTime)
			if err != nil {
				errorsFound <- err
				return
			}
			if created {
				inserted.Add(1)
			}
			if !hashPattern.MatchString(market.ManifestID) || market.MaxSpotSlippageBPS != 200 {
				errorsFound <- fmt.Errorf("invalid market result: %+v", market)
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	if inserted.Load() != 1 {
		t.Fatalf("inserted %d market rows, want 1", inserted.Load())
	}
	if writer.record.Review.PoolFee != 10_000 || writer.record.Review.PoolTickSpacing != 200 ||
		writer.record.Review.PoolID != marketPoolID || writer.record.Review.RouteSHA256 != scheduler.RouteSHA256 {
		t.Fatalf("market review lost route binding: %+v", writer.record.Review)
	}
}
