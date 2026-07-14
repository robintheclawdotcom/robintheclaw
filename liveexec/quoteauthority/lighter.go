package quoteauthority

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type bookOrder struct {
	Amount uint64
	Price  uint32
}

type lighterSnapshot struct {
	MarketIndex   uint32
	BaseDecimals  uint8
	PriceDecimals uint8
	MarkPrice     uint32
	Bids          []bookOrder
	Asks          []bookOrder
	PayloadSHA256 string
	ObservedAtMS  uint64
}

type lighterReader struct {
	baseURL       *url.URL
	marketIndex   uint32
	baseDecimals  uint8
	priceDecimals uint8
	client        *http.Client
	now           func() time.Time
}

func newLighterReader(config LiveAdapterConfig, client *http.Client, now func() time.Time) (*lighterReader, error) {
	endpoint, err := endpointURL(config.LighterAPIURL, true)
	if err != nil {
		return nil, err
	}
	return &lighterReader{
		baseURL: endpoint, marketIndex: config.LighterMarketIndex, baseDecimals: config.LighterBaseDecimals,
		priceDecimals: config.LighterPriceDecimals, client: client, now: now,
	}, nil
}

func (r *lighterReader) snapshot(ctx context.Context) (lighterSnapshot, error) {
	type response struct {
		body       []byte
		receivedAt uint64
		err        error
	}
	detailsCh := make(chan response, 1)
	bookCh := make(chan response, 1)
	go func() {
		body, receivedAt, err := r.fetch(ctx, "/api/v1/orderBookDetails", url.Values{
			"market_id": {strconv.FormatUint(uint64(r.marketIndex), 10)}, "filter": {"perp"},
		})
		detailsCh <- response{body, receivedAt, err}
	}()
	go func() {
		body, receivedAt, err := r.fetch(ctx, "/api/v1/orderBookOrders", url.Values{
			"market_id": {strconv.FormatUint(uint64(r.marketIndex), 10)}, "limit": {"250"},
		})
		bookCh <- response{body, receivedAt, err}
	}()
	details := <-detailsCh
	book := <-bookCh
	if details.err != nil || book.err != nil {
		return lighterSnapshot{}, errors.New("official Lighter order book is unavailable")
	}
	market, err := r.parseMarket(details.body)
	if err != nil {
		return lighterSnapshot{}, err
	}
	bids, asks, err := r.parseBook(book.body)
	if err != nil {
		return lighterSnapshot{}, err
	}
	hash := sha256.New()
	_, _ = hash.Write(details.body)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(book.body)
	return lighterSnapshot{
		MarketIndex: r.marketIndex, BaseDecimals: r.baseDecimals, PriceDecimals: r.priceDecimals,
		MarkPrice: market.markPrice, Bids: bids, Asks: asks,
		PayloadSHA256: hex.EncodeToString(hash.Sum(nil)), ObservedAtMS: maxUint64(details.receivedAt, book.receivedAt),
	}, nil
}

func (r *lighterReader) fetch(ctx context.Context, path string, query url.Values) ([]byte, uint64, error) {
	endpoint := *r.baseURL
	endpoint.Path = path
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-cache")
	response, err := r.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("Lighter returned HTTP %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, 0, errors.New("Lighter response is not JSON")
	}
	if age := response.Header.Get("Age"); age != "" && age != "0" {
		return nil, 0, errors.New("Lighter response came from a stale cache")
	}
	serverDate, err := http.ParseTime(response.Header.Get("Date"))
	if err != nil {
		return nil, 0, errors.New("Lighter response has no authenticated server time")
	}
	now := r.now()
	age := now.Sub(serverDate)
	if age < -time.Second || age > maximumSourceAge {
		return nil, 0, errors.New("Lighter response server time is stale")
	}
	body, err := readBounded(response.Body, maximumLighterResponse)
	if err != nil {
		return nil, 0, err
	}
	return body, uint64(now.UnixMilli()), nil
}

type marketMetadata struct {
	markPrice uint32
}

func (r *lighterReader) parseMarket(body []byte) (marketMetadata, error) {
	var envelope struct {
		Code             int               `json:"code"`
		OrderBookDetails []json.RawMessage `json:"order_book_details"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil || decoder.Decode(&struct{}{}) != io.EOF || envelope.Code != 200 {
		return marketMetadata{}, errors.New("Lighter market metadata response is invalid")
	}
	var found *marketMetadata
	for _, raw := range envelope.OrderBookDetails {
		var value struct {
			Symbol                 string          `json:"symbol"`
			MarketID               uint32          `json:"market_id"`
			MarketType             string          `json:"market_type"`
			Status                 string          `json:"status"`
			SupportedSizeDecimals  uint8           `json:"supported_size_decimals"`
			SupportedPriceDecimals uint8           `json:"supported_price_decimals"`
			MarkPrice              json.RawMessage `json:"mark_price"`
		}
		if err := json.Unmarshal(raw, &value); err != nil || value.MarketID != r.marketIndex {
			continue
		}
		if found != nil || value.Symbol != "AAPL" || value.MarketType != "perp" || value.Status != "active" ||
			value.SupportedSizeDecimals != r.baseDecimals || value.SupportedPriceDecimals != r.priceDecimals {
			return marketMetadata{}, errors.New("reviewed Lighter AAPL market identity changed")
		}
		mark, err := decimalJSONToUint32(value.MarkPrice, r.priceDecimals)
		if err != nil || mark == 0 {
			return marketMetadata{}, errors.New("Lighter AAPL mark price is invalid")
		}
		found = &marketMetadata{markPrice: mark}
	}
	if found == nil {
		return marketMetadata{}, errors.New("reviewed Lighter AAPL market is unavailable")
	}
	return *found, nil
}

func (r *lighterReader) parseBook(body []byte) ([]bookOrder, []bookOrder, error) {
	var envelope struct {
		Code      int               `json:"code"`
		TotalAsks int               `json:"total_asks"`
		Asks      []json.RawMessage `json:"asks"`
		TotalBids int               `json:"total_bids"`
		Bids      []json.RawMessage `json:"bids"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Code != 200 ||
		envelope.TotalAsks < len(envelope.Asks) || envelope.TotalBids < len(envelope.Bids) ||
		len(envelope.Asks) == 0 || len(envelope.Bids) == 0 {
		return nil, nil, errors.New("Lighter AAPL order book response is invalid")
	}
	bids, err := r.parseOrders(envelope.Bids, true)
	if err != nil {
		return nil, nil, err
	}
	asks, err := r.parseOrders(envelope.Asks, false)
	if err != nil {
		return nil, nil, err
	}
	return bids, asks, nil
}

func (r *lighterReader) parseOrders(rawOrders []json.RawMessage, bids bool) ([]bookOrder, error) {
	orders := make([]bookOrder, 0, len(rawOrders))
	nowMS := r.now().UnixMilli()
	var previous uint32
	for index, raw := range rawOrders {
		var value struct {
			RemainingBaseAmount string `json:"remaining_base_amount"`
			Price               string `json:"price"`
			OrderExpiry         int64  `json:"order_expiry"`
			TransactionTime     int64  `json:"transaction_time"`
		}
		if err := json.Unmarshal(raw, &value); err != nil || value.TransactionTime <= 0 || value.TransactionTime > nowMS+1000 ||
			(value.OrderExpiry != 0 && value.OrderExpiry <= nowMS) {
			return nil, errors.New("Lighter AAPL order book contains an invalid order")
		}
		amount, err := decimalToUint64(value.RemainingBaseAmount, r.baseDecimals)
		if err != nil || amount == 0 {
			return nil, errors.New("Lighter AAPL order amount is invalid")
		}
		price64, err := decimalToUint64(value.Price, r.priceDecimals)
		if err != nil || price64 == 0 || price64 > uint64(^uint32(0)) {
			return nil, errors.New("Lighter AAPL order price is invalid")
		}
		price := uint32(price64)
		if index > 0 && ((bids && price > previous) || (!bids && price < previous)) {
			return nil, errors.New("Lighter AAPL order book is unsorted")
		}
		previous = price
		orders = append(orders, bookOrder{Amount: amount, Price: price})
	}
	return orders, nil
}

func (s lighterSnapshot) executablePrice(side string, amount uint64) (uint32, error) {
	orders := s.Bids
	if side == "ask" {
		orders = s.Asks
	} else if side != "bid" {
		return 0, errors.New("unsupported Lighter quote side")
	}
	if len(orders) == 0 {
		return 0, errors.New("Lighter AAPL order book has no executable depth")
	}
	if amount == 0 {
		return orders[0].Price, nil
	}
	remaining := amount
	for _, order := range orders {
		if order.Amount >= remaining {
			return order.Price, nil
		}
		remaining -= order.Amount
	}
	return 0, errors.New("Lighter AAPL order book depth is insufficient")
}

func decimalJSONToUint32(raw json.RawMessage, decimals uint8) (uint32, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 0, errors.New("missing decimal")
	}
	value := strings.TrimSpace(string(raw))
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
	}
	parsed, err := decimalToUint64(value, decimals)
	if err != nil || parsed > uint64(^uint32(0)) {
		return 0, errors.New("decimal exceeds uint32")
	}
	return uint32(parsed), nil
}

func decimalToUint64(value string, decimals uint8) (uint64, error) {
	if value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "eE") {
		return 0, errors.New("invalid unsigned decimal")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		return 0, errors.New("invalid unsigned decimal")
	}
	for _, part := range parts {
		for _, char := range part {
			if char < '0' || char > '9' {
				return 0, errors.New("invalid unsigned decimal")
			}
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > int(decimals) {
		return 0, errors.New("decimal precision exceeds market scale")
	}
	fraction += strings.Repeat("0", int(decimals)-len(fraction))
	digits := strings.TrimLeft(parts[0]+fraction, "0")
	if digits == "" {
		return 0, nil
	}
	parsed, ok := new(big.Int).SetString(digits, 10)
	if !ok || !parsed.IsUint64() {
		return 0, errors.New("decimal exceeds uint64")
	}
	return parsed.Uint64(), nil
}
