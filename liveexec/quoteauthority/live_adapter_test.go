package quoteauthority

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

type episodeStub struct {
	episode OpenEpisode
}

func (s episodeStub) Resolve(context.Context, string, string) (OpenEpisode, error) {
	return s.episode, nil
}

type adapterTransport struct {
	now               time.Time
	perpPrice         string
	bookAmount        string
	oracleRound       *big.Int
	secondaryMismatch bool
	secondaryHeadLag  bool
	lighterStatus     int
	lighterDateOffset time.Duration
}

func (t *adapterTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host == "lighter.invalid" {
		return t.lighterResponse(request), nil
	}
	body, _ := io.ReadAll(request.Body)
	var calls []struct {
		ID     int             `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &calls); err != nil {
		return nil, err
	}
	responses := make([]rpcResponse, 0, len(calls))
	for _, call := range calls {
		result, err := t.rpcResult(request.URL.Host, call.Method, call.Params)
		if err != nil {
			return nil, err
		}
		responses = append(responses, rpcResponse{JSONRPC: "2.0", ID: call.ID, Result: result})
	}
	return jsonResponse(http.StatusOK, responses, t.now), nil
}

func (t *adapterTransport) rpcResult(host, method string, params json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "eth_chainId":
		return rawJSON("0x1237"), nil
	case "eth_getBlockByNumber":
		var values []json.RawMessage
		if err := json.Unmarshal(params, &values); err != nil || len(values) != 2 {
			return nil, fmt.Errorf("invalid block params")
		}
		var selector string
		if err := json.Unmarshal(values[0], &selector); err != nil {
			return nil, err
		}
		number := "0x100"
		if selector == "latest" && t.secondaryHeadLag && host == "rpc-one.invalid" {
			number = "0x101"
		}
		hash := testHash("block-" + number)
		if t.secondaryMismatch && host == "rpc-two.invalid" {
			hash = testHash("other-" + number)
		}
		return rawObject(map[string]any{
			"number": number, "hash": hash, "timestamp": fmt.Sprintf("0x%x", t.now.Unix()),
		}), nil
	case "eth_getCode":
		return rawJSON("0x60016000"), nil
	case "eth_call":
		var values []json.RawMessage
		if err := json.Unmarshal(params, &values); err != nil || len(values) != 2 {
			return nil, fmt.Errorf("invalid eth_call params")
		}
		var call struct {
			To   string `json:"to"`
			Data string `json:"data"`
		}
		if err := json.Unmarshal(values[0], &call); err != nil {
			return nil, err
		}
		return t.callResult(call.To, call.Data)
	default:
		return nil, fmt.Errorf("unexpected RPC method %s", method)
	}
}

func (t *adapterTransport) callResult(target, data string) (json.RawMessage, error) {
	switch data {
	case selectorDecimals:
		decimals := uint64(8)
		if target == protocol.SettlementToken {
			decimals = 6
		} else if target == protocol.StockToken {
			decimals = 18
		}
		return rawJSON(hexReturn(new(big.Int).SetUint64(decimals))), nil
	case selectorUIMultiplier, selectorNewUIMultiplier:
		return rawJSON(hexReturn(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))), nil
	case selectorEffectiveAt, selectorOraclePaused:
		return rawJSON(hexReturn(big.NewInt(0))), nil
	case selectorLatestRoundData:
		round := t.oracleRound
		if round == nil {
			round = big.NewInt(7)
		}
		words := []*big.Int{
			round, big.NewInt(2_500_000_000), big.NewInt(t.now.Unix() - 1),
			big.NewInt(t.now.Unix() - 1), round,
		}
		return rawJSON(hexWords(words...)), nil
	}
	if !strings.HasPrefix(data, "0x"+selectorExactInputQuote) {
		return nil, fmt.Errorf("unexpected eth_call data %s", data)
	}
	encoded, err := hex.DecodeString(data[2:])
	if err != nil || len(encoded) < 260 {
		return nil, fmt.Errorf("invalid quote calldata")
	}
	input := new(big.Int).SetBytes(encoded[228:260])
	output := big.NewInt(25_000_000)
	if input.BitLen() < 60 {
		output.Mul(input, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
		output.Div(output, big.NewInt(25_000_000))
	}
	return rawJSON(hexWords(output, big.NewInt(100_000))), nil
}

func (t *adapterTransport) lighterResponse(request *http.Request) *http.Response {
	status := t.lighterStatus
	if status == 0 {
		status = http.StatusOK
	}
	price := t.perpPrice
	if price == "" {
		price = "25.000"
	}
	amount := t.bookAmount
	if amount == "" {
		amount = "1.0000"
	}
	var body any
	switch request.URL.Path {
	case "/api/v1/orderBookDetails":
		body = map[string]any{
			"code": 200,
			"order_book_details": []any{map[string]any{
				"symbol": "AAPL", "market_id": 101, "market_type": "perp", "status": "active",
				"supported_size_decimals": 4, "supported_price_decimals": 3, "mark_price": price,
			}},
		}
	case "/api/v1/orderBookOrders":
		order := func(price string) map[string]any {
			return map[string]any{
				"remaining_base_amount": amount, "price": price,
				"order_expiry": t.now.UnixMilli() + 60_000, "transaction_time": t.now.UnixMilli() - 100,
			}
		}
		body = map[string]any{
			"code": 200, "total_asks": 1, "asks": []any{order("25.100")},
			"total_bids": 1, "bids": []any{order(price)},
		}
	default:
		body = map[string]any{"code": 400}
	}
	return jsonResponse(status, body, t.now.Add(t.lighterDateOffset))
}

func TestLiveAdapterBuildsMatchedEntryBelowEveryCap(t *testing.T) {
	adapter := testLiveAdapter(t, &adapterTransport{now: testAdapterTime, perpPrice: "25.100"}, nil)
	result, err := adapter.Quote(context.Background(), AdapterRequest{
		RequestID: testHash("live-entry"), ExecutionAccountID: "account-canary-1",
		MarketManifest: testHash("market"), Action: protocol.ActionEntry, EntryNotional: protocol.EntryNotionalMicros,
	})
	if err != nil {
		t.Fatal(err)
	}
	settlement, _ := new(big.Int).SetString(result.Spot.SettlementAmount, 10)
	perp := quoteNotional(result.Perp.BaseAmount, result.Perp.BaseDecimals, maxUint32(result.Perp.LimitPrice, result.Perp.MarkPrice), result.Perp.PriceDecimals)
	cap := new(big.Int).SetUint64(protocol.EntryNotionalMicros)
	if settlement.Cmp(cap) >= 0 || perp.Cmp(cap) > 0 || new(big.Int).Add(settlement, perp).Cmp(new(big.Int).Mul(cap, big.NewInt(2))) > 0 {
		t.Fatalf("resized entry exceeded caps: spot=%s perp=%s", settlement, perp)
	}
	if result.Perp.BaseAmount == 0 || result.DurableSource.EventID != testHash("live-entry") {
		t.Fatal("entry quote lost matched exposure or deterministic persistence identity")
	}
}

func TestLiveAdapterUnwindUsesStockInputForExposureAndUSDGOutputForMinimum(t *testing.T) {
	intentID := testHash("open-intent")
	resolver := episodeStub{episode: OpenEpisode{
		SchemaVersion: 1, ExecutionAccountID: "account-canary-1", IntentID: intentID,
		Phase: "perp_and_spot", SpotAmount: "1000000000000000000", PerpBaseAmount: 10_000,
		ObservedAtMS: uint64(testAdapterTime.UnixMilli()),
	}}
	adapter := testLiveAdapter(t, &adapterTransport{now: testAdapterTime}, resolver)
	result, err := adapter.Quote(context.Background(), AdapterRequest{
		RequestID: testHash("live-unwind"), ExecutionAccountID: "account-canary-1", IntentID: intentID,
		MarketManifest: testHash("market"), Action: protocol.ActionUnwind,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Perp.BaseAmount != 10_000 || result.Spot.StockAmount != "1000000000000000000" ||
		result.Spot.SettlementAmount != "25000000" || result.Spot.MinimumAmountOut != "24750000" {
		t.Fatalf("unwind mixed stock exposure with USDG output: %+v", result)
	}
}

func TestLiveAdapterSupportsSpotOnlyRefresh(t *testing.T) {
	intentID := testHash("spot-only-intent")
	resolver := episodeStub{episode: OpenEpisode{
		SchemaVersion: 1, ExecutionAccountID: "account-canary-1", IntentID: intentID,
		Phase: "spot_only", SpotAmount: "1000000000000000000", PerpBaseAmount: 0,
		ObservedAtMS: uint64(testAdapterTime.UnixMilli()),
	}}
	adapter := testLiveAdapter(t, &adapterTransport{now: testAdapterTime}, resolver)
	result, err := adapter.Quote(context.Background(), AdapterRequest{
		RequestID: testHash("spot-only-refresh"), ExecutionAccountID: "account-canary-1", IntentID: intentID,
		MarketManifest: testHash("market"), Action: protocol.ActionUnwind,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Perp.Phase != "spot_only" || result.Perp.BaseAmount != 0 || result.Perp.LimitPrice == 0 {
		t.Fatalf("spot-only refresh lost phase evidence: %+v", result.Perp)
	}
}

func TestLiveAdapterFailsClosedOnRPCDisagreementAndLighterDepth(t *testing.T) {
	t.Run("rpc disagreement", func(t *testing.T) {
		adapter := testLiveAdapter(t, &adapterTransport{now: testAdapterTime, secondaryMismatch: true}, nil)
		_, err := adapter.Quote(context.Background(), AdapterRequest{
			RequestID: testHash("rpc-disagree"), ExecutionAccountID: "account-canary-1",
			MarketManifest: testHash("market"), Action: protocol.ActionEntry, EntryNotional: protocol.EntryNotionalMicros,
		})
		if err == nil {
			t.Fatal("RPC disagreement was accepted")
		}
	})
	t.Run("insufficient depth", func(t *testing.T) {
		adapter := testLiveAdapter(t, &adapterTransport{now: testAdapterTime, bookAmount: "0.1000"}, nil)
		_, err := adapter.Quote(context.Background(), AdapterRequest{
			RequestID: testHash("thin-book"), ExecutionAccountID: "account-canary-1",
			MarketManifest: testHash("market"), Action: protocol.ActionEntry, EntryNotional: protocol.EntryNotionalMicros,
		})
		if err == nil {
			t.Fatal("insufficient Lighter depth was accepted")
		}
	})
}

func TestLiveAdapterUsesNewestCanonicalBlockSharedByBothRPCs(t *testing.T) {
	adapter := testLiveAdapter(t, &adapterTransport{now: testAdapterTime, secondaryHeadLag: true}, nil)
	result, err := adapter.Quote(context.Background(), AdapterRequest{
		RequestID: testHash("common-head"), ExecutionAccountID: "account-canary-1",
		MarketManifest: testHash("market"), Action: protocol.ActionEntry, EntryNotional: protocol.EntryNotionalMicros,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Spot.BlockHash != testHash("block-0x100") {
		t.Fatalf("quote used an unshared head: %s", result.Spot.BlockHash)
	}
}

func TestLiveAdapterRequotePreservesFinalizedBlockAge(t *testing.T) {
	transport := &adapterTransport{now: testAdapterTime}
	adapter := testLiveAdapter(t, transport, nil)
	observedAt := uint64(testAdapterTime.Add(-4 * time.Second).UnixMilli())
	snapshot, err := adapter.chain.requote(context.Background(), chainSnapshot{
		ObservedAtMS: observedAt,
		blockSelector: map[string]any{
			"blockHash": testHash("fixed-requote-block"), "requireCanonical": true,
		},
	}, big.NewInt(1_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ObservedAtMS != observedAt {
		t.Fatalf("requote refreshed finalized source age: got %d want %d", snapshot.ObservedAtMS, observedAt)
	}
}

func TestCanonicalPoolIdentityIsPinned(t *testing.T) {
	actual, err := canonicalPoolID()
	if err != nil {
		t.Fatal(err)
	}
	if actual != poolID {
		t.Fatalf("canonical pool id %s does not match %s", actual, poolID)
	}
}

func TestLiveAdapterPreservesUint80OracleRound(t *testing.T) {
	round := new(big.Int).Lsh(big.NewInt(1), 64)
	round.Add(round, big.NewInt(7))
	adapter := testLiveAdapter(t, &adapterTransport{now: testAdapterTime, oracleRound: round}, nil)
	result, err := adapter.Quote(context.Background(), AdapterRequest{
		RequestID: testHash("uint80-round"), ExecutionAccountID: "account-canary-1",
		MarketManifest: testHash("market"), Action: protocol.ActionEntry,
		EntryNotional: protocol.EntryNotionalMicros,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source.OracleRound != round.String() || result.Spot.MinOracleRoundID != round.String() {
		t.Fatalf("oracle round was truncated: source=%s minimum=%s", result.Source.OracleRound, result.Spot.MinOracleRoundID)
	}
}

var testAdapterTime = time.Unix(1_800_000_000, 0).UTC()

func testLiveAdapter(t *testing.T, transport *adapterTransport, resolver OpenEpisodeResolver) *LiveAdapter {
	t.Helper()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	config := LiveAdapterConfig{
		PrimaryRPCURL: "https://rpc-one.invalid", SecondaryRPCURL: "https://rpc-two.invalid",
		LighterAPIURL: "https://lighter.invalid", ReferenceFeed: "0x1111111111111111111111111111111111111111",
		ReferenceFeedCodeHash: "0x" + strings.Repeat("1", 64), ReferenceFeedDecimals: 8,
		ReferenceFeedHeartbeat: 25 * time.Hour, LighterMarketIndex: 101, LighterBaseDecimals: 4, LighterPriceDecimals: 3,
	}
	adapter, err := newLiveAdapter(config, resolver, client, func() time.Time { return transport.now })
	if err != nil {
		t.Fatal(err)
	}
	codeHash := runtimeCodeHash("0x60016000")
	for address := range adapter.chain.expectedCodeHashes {
		adapter.chain.expectedCodeHashes[address] = codeHash
	}
	return adapter
}

func rawJSON(value string) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func rawObject(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func hexReturn(value *big.Int) string {
	return "0x" + hex.EncodeToString(uintWord(value))
}

func hexWords(values ...*big.Int) string {
	var encoded []byte
	for _, value := range values {
		encoded = append(encoded, uintWord(value)...)
	}
	return "0x" + hex.EncodeToString(encoded)
}

func jsonResponse(status int, value any, date time.Time) *http.Response {
	body, _ := json.Marshal(value)
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": {"application/json"}, "Date": {date.Format(http.TimeFormat)},
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	}
}
