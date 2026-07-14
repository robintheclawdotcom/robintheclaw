package quoteauthority

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"

	"github.com/robin-the-claw/liveexec/protocol"
)

type rpcCall struct {
	ID     int
	Method string
	Params any
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type rpcEndpoint struct {
	url    string
	client *http.Client
}

type dualRPC struct {
	primary   rpcEndpoint
	secondary rpcEndpoint
}

type canonicalBlock struct {
	Number        string
	Hash          string
	Timestamp     uint64
	ObservedAt    uint64
	blockSelector map[string]any
}

type chainSnapshot struct {
	BlockHash            string
	BlockTimestamp       uint64
	ObservedAtMS         uint64
	InputAmount          *big.Int
	SpotAmountOut        *big.Int
	UIMultiplier         *big.Int
	OracleRound          *big.Int
	ReferencePriceMicros uint64
	blockSelector        map[string]any
}

type chainReader struct {
	rpc                    dualRPC
	referenceFeed          string
	expectedCodeHashes     map[string]string
	referenceFeedDecimals  uint8
	referenceFeedHeartbeat time.Duration
	now                    func() time.Time
}

func newChainReader(config LiveAdapterConfig, client *http.Client, now func() time.Time) (*chainReader, error) {
	if _, err := endpointURL(config.PrimaryRPCURL, false); err != nil {
		return nil, err
	}
	if _, err := endpointURL(config.SecondaryRPCURL, false); err != nil {
		return nil, err
	}
	return &chainReader{
		rpc: dualRPC{
			primary:   rpcEndpoint{url: config.PrimaryRPCURL, client: client},
			secondary: rpcEndpoint{url: config.SecondaryRPCURL, client: client},
		},
		referenceFeed: config.ReferenceFeed,
		expectedCodeHashes: map[string]string{
			quoterAddress: quoterCodeHash, poolManagerAddress: poolManagerCodeHash,
			protocol.SettlementToken: settlementCodeHash, protocol.StockToken: stockCodeHash,
			config.ReferenceFeed: config.ReferenceFeedCodeHash,
		},
		referenceFeedDecimals: config.ReferenceFeedDecimals, referenceFeedHeartbeat: config.ReferenceFeedHeartbeat,
		now: now,
	}, nil
}

func (r *chainReader) snapshot(ctx context.Context, action protocol.Action, input *big.Int) (chainSnapshot, error) {
	block, err := r.rpc.canonicalHead(ctx, r.now)
	if err != nil {
		return chainSnapshot{}, err
	}
	quoteData, err := encodeExactInputQuote(action == protocol.ActionEntry, input)
	if err != nil {
		return chainSnapshot{}, err
	}
	selector := block.blockSelector
	calls := []rpcCall{
		{1, "eth_getCode", []any{quoterAddress, selector}},
		{2, "eth_getCode", []any{poolManagerAddress, selector}},
		{3, "eth_getCode", []any{protocol.SettlementToken, selector}},
		{4, "eth_getCode", []any{protocol.StockToken, selector}},
		{5, "eth_getCode", []any{r.referenceFeed, selector}},
		{6, "eth_call", []any{callObject(protocol.SettlementToken, selectorDecimals), selector}},
		{7, "eth_call", []any{callObject(protocol.StockToken, selectorDecimals), selector}},
		{8, "eth_call", []any{callObject(r.referenceFeed, selectorDecimals), selector}},
		{9, "eth_call", []any{callObject(protocol.StockToken, selectorUIMultiplier), selector}},
		{10, "eth_call", []any{callObject(protocol.StockToken, selectorNewUIMultiplier), selector}},
		{11, "eth_call", []any{callObject(protocol.StockToken, selectorEffectiveAt), selector}},
		{12, "eth_call", []any{callObject(protocol.StockToken, selectorOraclePaused), selector}},
		{13, "eth_call", []any{callObject(r.referenceFeed, selectorLatestRoundData), selector}},
		{14, "eth_call", []any{callObject(quoterAddress, quoteData), selector}},
	}
	results, err := r.rpc.batch(ctx, calls)
	if err != nil {
		return chainSnapshot{}, err
	}
	for _, expected := range []struct {
		id      int
		address string
		name    string
	}{
		{1, quoterAddress, "quoter"}, {2, poolManagerAddress, "pool manager"},
		{3, protocol.SettlementToken, "USDG"}, {4, protocol.StockToken, "AAPL"}, {5, r.referenceFeed, "AAPL reference feed"},
	} {
		code, err := resultString(results[expected.id])
		if err != nil || !strings.EqualFold(runtimeCodeHash(code), r.expectedCodeHashes[expected.address]) {
			return chainSnapshot{}, fmt.Errorf("%s code hash mismatch", expected.name)
		}
	}
	settlementDec, err := decodeUint8Result(results[6])
	if err != nil || settlementDec != settlementDecimals {
		return chainSnapshot{}, errors.New("USDG decimals changed")
	}
	stockDec, err := decodeUint8Result(results[7])
	if err != nil || stockDec != stockDecimals {
		return chainSnapshot{}, errors.New("AAPL decimals changed")
	}
	feedDec, err := decodeUint8Result(results[8])
	if err != nil || feedDec != r.referenceFeedDecimals {
		return chainSnapshot{}, errors.New("AAPL reference feed decimals changed")
	}
	multiplier, err := decodeUintResult(results[9])
	if err != nil || multiplier.Sign() <= 0 {
		return chainSnapshot{}, errors.New("AAPL UI multiplier is invalid")
	}
	nextMultiplier, err := decodeUintResult(results[10])
	if err != nil || multiplier.Cmp(nextMultiplier) != 0 {
		return chainSnapshot{}, errors.New("AAPL UI multiplier is transitioning")
	}
	if _, err := decodeUintResult(results[11]); err != nil {
		return chainSnapshot{}, errors.New("AAPL multiplier effective time is invalid")
	}
	paused, err := decodeBoolResult(results[12])
	if err != nil || paused {
		return chainSnapshot{}, errors.New("AAPL oracle is paused")
	}
	oracle, err := decodeRoundData(results[13])
	if err != nil || oracle.RoundID.Sign() <= 0 || oracle.Answer.Sign() <= 0 || oracle.AnsweredInRound.Cmp(oracle.RoundID) < 0 ||
		oracle.UpdatedAt == 0 || oracle.UpdatedAt > block.Timestamp ||
		block.Timestamp-oracle.UpdatedAt > uint64(r.referenceFeedHeartbeat/time.Second) {
		return chainSnapshot{}, errors.New("AAPL reference oracle is invalid or stale")
	}
	price, err := scaleToMicros(oracle.Answer, r.referenceFeedDecimals)
	if err != nil {
		return chainSnapshot{}, err
	}
	amountOut, err := decodeQuoteResult(results[14])
	if err != nil || amountOut.Sign() <= 0 {
		return chainSnapshot{}, errors.New("Uniswap v4 executable quote is invalid")
	}
	return chainSnapshot{
		BlockHash: block.Hash, BlockTimestamp: block.Timestamp, ObservedAtMS: block.ObservedAt,
		InputAmount: new(big.Int).Set(input), SpotAmountOut: amountOut, UIMultiplier: multiplier,
		OracleRound: oracle.RoundID, ReferencePriceMicros: price, blockSelector: selector,
	}, nil
}

func (r *chainReader) requote(ctx context.Context, snapshot chainSnapshot, input *big.Int) (chainSnapshot, error) {
	data, err := encodeExactInputQuote(true, input)
	if err != nil {
		return chainSnapshot{}, err
	}
	results, err := r.rpc.batch(ctx, []rpcCall{{1, "eth_call", []any{callObject(quoterAddress, data), snapshot.blockSelector}}})
	if err != nil {
		return chainSnapshot{}, err
	}
	amountOut, err := decodeQuoteResult(results[1])
	if err != nil || amountOut.Sign() <= 0 {
		return chainSnapshot{}, errors.New("Uniswap v4 resized quote is invalid")
	}
	snapshot.InputAmount = new(big.Int).Set(input)
	snapshot.SpotAmountOut = amountOut
	return snapshot, nil
}

func (d dualRPC) canonicalHead(ctx context.Context, now func() time.Time) (canonicalBlock, error) {
	calls := []rpcCall{{1, "eth_chainId", []any{}}, {2, "eth_getBlockByNumber", []any{"latest", false}}}
	type response struct {
		results map[int]json.RawMessage
		err     error
	}
	primaryCh := make(chan response, 1)
	secondaryCh := make(chan response, 1)
	go func() { value, err := d.primary.batch(ctx, calls); primaryCh <- response{value, err} }()
	go func() { value, err := d.secondary.batch(ctx, calls); secondaryCh <- response{value, err} }()
	primary := <-primaryCh
	secondary := <-secondaryCh
	if primary.err != nil || secondary.err != nil {
		return canonicalBlock{}, errors.New("both Robinhood RPCs must return current chain state")
	}
	primaryChain, err := resultString(primary.results[1])
	if err != nil {
		return canonicalBlock{}, errors.New("primary Robinhood RPC chain id is invalid")
	}
	secondaryChain, err := resultString(secondary.results[1])
	if err != nil || primaryChain != secondaryChain {
		return canonicalBlock{}, errors.New("Robinhood RPC chain ids disagree")
	}
	chainID, err := strconv.ParseUint(strings.TrimPrefix(primaryChain, "0x"), 16, 64)
	if err != nil || chainID != protocol.ChainID {
		return canonicalBlock{}, errors.New("Robinhood RPC is on the wrong chain")
	}
	primaryHead, err := parseCanonicalBlock(primary.results[2])
	if err != nil {
		return canonicalBlock{}, err
	}
	secondaryHead, err := parseCanonicalBlock(secondary.results[2])
	if err != nil {
		return canonicalBlock{}, err
	}
	primaryNumber, _ := strconv.ParseUint(strings.TrimPrefix(primaryHead.Number, "0x"), 16, 64)
	secondaryNumber, _ := strconv.ParseUint(strings.TrimPrefix(secondaryHead.Number, "0x"), 16, 64)
	commonNumber := primaryNumber
	if secondaryNumber < commonNumber {
		commonNumber = secondaryNumber
	}
	selector := fmt.Sprintf("0x%x", commonNumber)
	commonCall := []rpcCall{{1, "eth_getBlockByNumber", []any{selector, false}}}
	primaryCh = make(chan response, 1)
	secondaryCh = make(chan response, 1)
	go func() { value, err := d.primary.batch(ctx, commonCall); primaryCh <- response{value, err} }()
	go func() { value, err := d.secondary.batch(ctx, commonCall); secondaryCh <- response{value, err} }()
	primary = <-primaryCh
	secondary = <-secondaryCh
	if primary.err != nil || secondary.err != nil {
		return canonicalBlock{}, errors.New("Robinhood RPC common head is unavailable")
	}
	first, err := parseCanonicalBlock(primary.results[1])
	if err != nil {
		return canonicalBlock{}, err
	}
	second, err := parseCanonicalBlock(secondary.results[1])
	if err != nil || first.Number != second.Number || first.Hash != second.Hash || first.Timestamp != second.Timestamp {
		return canonicalBlock{}, errors.New("Robinhood RPCs disagree on their newest common block")
	}
	nowTime := now()
	nowSeconds := uint64(nowTime.Unix())
	if first.Timestamp > nowSeconds+1 || nowSeconds-first.Timestamp > uint64(maximumSourceAge/time.Second) {
		return canonicalBlock{}, errors.New("Robinhood common block is stale")
	}
	first.ObservedAt = first.Timestamp * 1_000
	first.blockSelector = map[string]any{"blockHash": first.Hash, "requireCanonical": true}
	return first, nil
}

func (d dualRPC) batch(ctx context.Context, calls []rpcCall) (map[int]json.RawMessage, error) {
	type response struct {
		results map[int]json.RawMessage
		err     error
	}
	primaryCh := make(chan response, 1)
	secondaryCh := make(chan response, 1)
	go func() { value, err := d.primary.batch(ctx, calls); primaryCh <- response{value, err} }()
	go func() { value, err := d.secondary.batch(ctx, calls); secondaryCh <- response{value, err} }()
	primary := <-primaryCh
	secondary := <-secondaryCh
	if primary.err != nil || secondary.err != nil {
		return nil, errors.New("Robinhood dual-RPC request failed")
	}
	for _, call := range calls {
		left, leftOK := primary.results[call.ID]
		right, rightOK := secondary.results[call.ID]
		if !leftOK || !rightOK || !equalJSON(left, right) {
			return nil, fmt.Errorf("Robinhood RPC disagreement for %s", call.Method)
		}
	}
	return primary.results, nil
}

func (e rpcEndpoint) batch(ctx context.Context, calls []rpcCall) (map[int]json.RawMessage, error) {
	requests := make([]rpcRequest, len(calls))
	for index, call := range calls {
		requests[index] = rpcRequest{JSONRPC: "2.0", ID: call.ID, Method: call.Method, Params: call.Params}
	}
	body, err := json.Marshal(requests)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := e.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RPC returned HTTP %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, errors.New("RPC response is not JSON")
	}
	encoded, err := readBounded(response.Body, maximumRPCResponse)
	if err != nil {
		return nil, err
	}
	var values []rpcResponse
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := decoder.Decode(&values); err != nil || decoder.Decode(&struct{}{}) != io.EOF || len(values) != len(calls) {
		return nil, errors.New("RPC response schema is invalid")
	}
	results := make(map[int]json.RawMessage, len(values))
	for _, value := range values {
		if value.JSONRPC != "2.0" || value.Error != nil || len(value.Result) == 0 {
			return nil, errors.New("RPC returned an error")
		}
		if _, exists := results[value.ID]; exists {
			return nil, errors.New("RPC returned a duplicate id")
		}
		results[value.ID] = append(json.RawMessage(nil), value.Result...)
	}
	return results, nil
}

func parseCanonicalBlock(raw json.RawMessage) (canonicalBlock, error) {
	var value struct {
		Number    string `json:"number"`
		Hash      string `json:"hash"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(raw, &value); err != nil || value.Number == "" || !validHash(value.Hash) {
		return canonicalBlock{}, errors.New("Robinhood canonical block is invalid")
	}
	if _, err := strconv.ParseUint(strings.TrimPrefix(value.Number, "0x"), 16, 64); err != nil {
		return canonicalBlock{}, errors.New("Robinhood canonical block number is invalid")
	}
	timestamp, err := strconv.ParseUint(strings.TrimPrefix(value.Timestamp, "0x"), 16, 64)
	if err != nil || timestamp == 0 {
		return canonicalBlock{}, errors.New("Robinhood canonical block timestamp is invalid")
	}
	return canonicalBlock{Number: value.Number, Hash: value.Hash, Timestamp: timestamp}, nil
}

type roundData struct {
	RoundID         *big.Int
	Answer          *big.Int
	UpdatedAt       uint64
	AnsweredInRound *big.Int
}

func decodeRoundData(raw json.RawMessage) (roundData, error) {
	data, err := decodeHexResult(raw)
	if err != nil || len(data) != 160 {
		return roundData{}, errors.New("oracle round response is invalid")
	}
	round, ok := unsignedWord(data[0:32], 80)
	if !ok || data[32]&0x80 != 0 {
		return roundData{}, errors.New("oracle round response is invalid")
	}
	answer := new(big.Int).SetBytes(data[32:64])
	updated, updatedOK := uint64Word(data[96:128], 64)
	answered, answeredOK := unsignedWord(data[128:160], 80)
	if !updatedOK || !answeredOK {
		return roundData{}, errors.New("oracle round response is invalid")
	}
	return roundData{RoundID: round, Answer: answer, UpdatedAt: updated, AnsweredInRound: answered}, nil
}

func decodeQuoteResult(raw json.RawMessage) (*big.Int, error) {
	data, err := decodeHexResult(raw)
	if err != nil || len(data) != 64 {
		return nil, errors.New("quoter response is invalid")
	}
	return new(big.Int).SetBytes(data[:32]), nil
}

func decodeUintResult(raw json.RawMessage) (*big.Int, error) {
	data, err := decodeHexResult(raw)
	if err != nil || len(data) != 32 {
		return nil, errors.New("uint256 response is invalid")
	}
	return new(big.Int).SetBytes(data), nil
}

func decodeUint8Result(raw json.RawMessage) (uint8, error) {
	value, err := decodeUintResult(raw)
	if err != nil || !value.IsUint64() || value.Uint64() > 255 {
		return 0, errors.New("uint8 response is invalid")
	}
	return uint8(value.Uint64()), nil
}

func decodeBoolResult(raw json.RawMessage) (bool, error) {
	value, err := decodeUintResult(raw)
	if err != nil || !value.IsUint64() || value.Uint64() > 1 {
		return false, errors.New("bool response is invalid")
	}
	return value.Uint64() == 1, nil
}

func decodeHexResult(raw json.RawMessage) ([]byte, error) {
	value, err := resultString(raw)
	if err != nil || !strings.HasPrefix(value, "0x") || len(value)%2 != 0 {
		return nil, errors.New("hex RPC result is invalid")
	}
	return hex.DecodeString(value[2:])
}

func resultString(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func runtimeCodeHash(value string) string {
	if !strings.HasPrefix(value, "0x") {
		return ""
	}
	code, err := hex.DecodeString(value[2:])
	if err != nil || len(code) == 0 {
		return ""
	}
	hash := sha3.NewLegacyKeccak256()
	_, _ = hash.Write(code)
	return "0x" + hex.EncodeToString(hash.Sum(nil))
}

func scaleToMicros(value *big.Int, decimals uint8) (uint64, error) {
	result := new(big.Int).Set(value)
	if decimals < settlementDecimals {
		result.Mul(result, pow10(settlementDecimals-decimals))
	} else if decimals > settlementDecimals {
		result.Div(result, pow10(decimals-settlementDecimals))
	}
	if !result.IsUint64() || result.Sign() <= 0 {
		return 0, errors.New("reference price cannot be represented in micros")
	}
	return result.Uint64(), nil
}

func uint64Word(word []byte, bits uint) (uint64, bool) {
	value := new(big.Int).SetBytes(word)
	return value.Uint64(), value.IsUint64() && value.BitLen() <= int(bits)
}

func unsignedWord(word []byte, bits uint) (*big.Int, bool) {
	value := new(big.Int).SetBytes(word)
	return value, value.BitLen() <= int(bits)
}

func equalJSON(left, right json.RawMessage) bool {
	var leftBuffer, rightBuffer bytes.Buffer
	if json.Compact(&leftBuffer, left) != nil || json.Compact(&rightBuffer, right) != nil {
		return false
	}
	return bytes.Equal(leftBuffer.Bytes(), rightBuffer.Bytes())
}

func callObject(target, data string) map[string]string {
	return map[string]string{"to": target, "data": data}
}

func secureHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:             nil,
		DialContext:       (&net.Dialer{Timeout: time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true,
		MaxIdleConns:      16, MaxIdleConnsPerHost: 8, IdleConnTimeout: 60 * time.Second,
		TLSHandshakeTimeout: 2 * time.Second, ResponseHeaderTimeout: 2 * time.Second,
		ExpectContinueTimeout: time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}
