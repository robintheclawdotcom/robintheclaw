package publisher

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	usdgAddress             = "0x5fc5360d0400a0fd4f2af552add042d716f1d168"
	aaplAddress             = "0xaf3d76f1834a1d425780943c99ea8a608f8a93f9"
	zeroAddress             = "0x0000000000000000000000000000000000000000"
	maxFinalizedEvidenceAge = 30 * time.Minute
)

type RobinhoodClient struct {
	primary   *rpcEndpoint
	secondary *rpcEndpoint
	mu        sync.Mutex
	finalized map[string]blockRef
}

type rpcEndpoint struct {
	url    string
	host   string
	client *http.Client
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type blockRef struct {
	Number    uint64 `json:"number"`
	Hash      string `json:"hash"`
	Timestamp uint64 `json:"timestamp"`
}

type rpcBlock struct {
	Number    string `json:"number"`
	Hash      string `json:"hash"`
	Timestamp string `json:"timestamp"`
}

type rpcProof struct {
	Address  string `json:"address"`
	CodeHash string `json:"codeHash"`
}

type rpcReceipt struct {
	TransactionHash string `json:"transactionHash"`
	BlockHash       string `json:"blockHash"`
	BlockNumber     string `json:"blockNumber"`
	Status          string `json:"status"`
	To              string `json:"to"`
	ContractAddress string `json:"contractAddress"`
	Logs            []struct {
		Address string `json:"address"`
	} `json:"logs"`
}

type endpointObservation struct {
	Block                blockRef
	Owner                string
	Factory              string
	RiskManager          string
	SpotAdapter          string
	VaultOwner           string
	VaultAgent           string
	VaultRegistry        string
	VaultRiskManager     string
	VaultSpotAdapter     string
	VaultCodeHash        string
	AgentEnabled         bool
	Flat                 bool
	GlobalMode           uint64
	RiskMode             uint64
	SettlementBalanceRaw string
	StockBalanceRaw      string
	OwnerGasRaw          string
	SignerGasRaw         string
	SignerNonce          uint64
	SpotConfigVersion    uint64
	StockDecimals        uint8
	UIMultiplierE18      string
	NewUIMultiplierE18   string
	OraclePaused         bool
	OracleHealthy        bool
	SequencerHealthy     bool
	Receipts             []rpcReceipt
}

type endpointHeads struct {
	Finalized blockRef
	Safe      blockRef
	Latest    blockRef
}

func NewRobinhoodClient(primaryURL, secondaryURL string, client *http.Client) (*RobinhoodClient, error) {
	primary, err := newRPCEndpoint(primaryURL, client)
	if err != nil {
		return nil, err
	}
	secondary, err := newRPCEndpoint(secondaryURL, client)
	if err != nil {
		return nil, err
	}
	if primary.host == secondary.host {
		return nil, errors.New("Robinhood RPC endpoints must be independent")
	}
	return &RobinhoodClient{primary: primary, secondary: secondary, finalized: make(map[string]blockRef)}, nil
}

func newRPCEndpoint(rawURL string, client *http.Client) (*rpcEndpoint, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !strings.HasPrefix(rawURL, "http://127.0.0.1:")) {
		return nil, errors.New("Robinhood RPC URL must use HTTPS")
	}
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	}
	return &rpcEndpoint{url: rawURL, host: strings.ToLower(parsed.Hostname()), client: client}, nil
}

func (c *RobinhoodClient) Collect(ctx context.Context, binding RobinhoodBinding) (RobinhoodObservation, error) {
	if err := validateRobinhoodBinding(binding); err != nil {
		return RobinhoodObservation{}, err
	}
	c.mu.Lock()
	prior := c.finalized[binding.Vault]
	c.mu.Unlock()

	type headResult struct {
		heads endpointHeads
		err   error
	}
	headResults := make(chan headResult, 2)
	for _, endpoint := range []*rpcEndpoint{c.primary, c.secondary} {
		go func(endpoint *rpcEndpoint) {
			heads, err := endpoint.heads(ctx, prior)
			headResults <- headResult{heads: heads, err: err}
		}(endpoint)
	}
	firstHead, secondHead := <-headResults, <-headResults
	if firstHead.err != nil {
		return RobinhoodObservation{}, firstHead.err
	}
	if secondHead.err != nil {
		return RobinhoodObservation{}, secondHead.err
	}

	finalizedNumber := minimumBlockNumber(firstHead.heads.Finalized, secondHead.heads.Finalized)
	safeNumber := minimumBlockNumber(firstHead.heads.Safe, secondHead.heads.Safe)
	currentNumber := minimumBlockNumber(firstHead.heads.Latest, secondHead.heads.Latest)
	if finalizedNumber == 0 || safeNumber < finalizedNumber || currentNumber < safeNumber ||
		(prior.Number > 0 && finalizedNumber < prior.Number) {
		return RobinhoodObservation{}, errors.New("Robinhood RPC head ordering is unsafe")
	}

	finalizedBlock, err := c.commonBlock(ctx, finalizedNumber)
	if err != nil {
		return RobinhoodObservation{}, err
	}
	safeBlock, err := c.commonBlock(ctx, safeNumber)
	if err != nil {
		return RobinhoodObservation{}, err
	}
	currentBlock, err := c.commonBlock(ctx, currentNumber)
	if err != nil {
		return RobinhoodObservation{}, err
	}
	if !commonHeadMatches(finalizedBlock, firstHead.heads.Finalized, secondHead.heads.Finalized) ||
		!commonHeadMatches(safeBlock, firstHead.heads.Safe, secondHead.heads.Safe) ||
		!commonHeadMatches(currentBlock, firstHead.heads.Latest, secondHead.heads.Latest) {
		return RobinhoodObservation{}, errors.New("Robinhood RPC tagged head disagreement")
	}
	now := time.Now().UTC()
	if staleBlock(finalizedBlock, now, maxFinalizedEvidenceAge) {
		return RobinhoodObservation{}, errors.New("Robinhood finalized evidence is stale")
	}
	if staleBlock(safeBlock, now, maxFinalizedEvidenceAge) {
		return RobinhoodObservation{}, errors.New("Robinhood safe evidence is stale")
	}
	if staleBlock(currentBlock, now, maxEvidenceAge) {
		return RobinhoodObservation{}, errors.New("Robinhood current account state is stale")
	}
	if safeBlock.Number < finalizedBlock.Number || currentBlock.Number < safeBlock.Number ||
		safeBlock.Timestamp < finalizedBlock.Timestamp || currentBlock.Timestamp < safeBlock.Timestamp {
		return RobinhoodObservation{}, errors.New("Robinhood current account state predates finality evidence")
	}

	finalized, err := c.collectAt(ctx, binding, finalizedBlock, true)
	if err != nil {
		return RobinhoodObservation{}, err
	}
	current := finalized
	if currentBlock != finalizedBlock {
		current, err = c.collectAt(ctx, binding, currentBlock, false)
		if err != nil {
			return RobinhoodObservation{}, err
		}
	}

	wiring := immutableWiringMatches(finalized, binding) &&
		validModes(finalized) && currentWiringMatches(current, binding)
	finality := receiptsFinal(finalized.Receipts, finalizedBlock) && receiptsBound(finalized.Receipts, binding)
	if finality {
		c.mu.Lock()
		c.finalized[binding.Vault] = finalizedBlock
		c.mu.Unlock()
	}
	return RobinhoodObservation{
		Vault: binding.Vault, Signer: binding.Signer, Owner: binding.Owner,
		SettlementBalanceRaw: current.SettlementBalanceRaw, OwnerGasRaw: current.OwnerGasRaw, SignerGasRaw: current.SignerGasRaw,
		AgentEnabled: current.AgentEnabled, FinalizedAgentAddress: finalized.VaultAgent,
		FinalizedAgentEnabled: finalized.AgentEnabled,
		FinalizedAgentRevoked: agentRevoked(finalized), Flat: current.Flat && current.StockBalanceRaw == "0",
		WiringVerified: wiring, FinalityHealthy: finality,
		FundingReady:   decimalAtLeast(current.SettlementBalanceRaw, binding.MinimumSettlementRaw),
		OwnerGasReady:  decimalAtLeast(current.OwnerGasRaw, binding.MinimumOwnerGasRaw),
		SignerGasReady: decimalAtLeast(current.SignerGasRaw, binding.MinimumSignerGasRaw),
		GlobalMode:     modeName(current.GlobalMode), FinalizedGlobalMode: modeName(finalized.GlobalMode),
		RiskMode: modeName(current.RiskMode), FinalizedRiskMode: modeName(finalized.RiskMode),
		FinalizedNumber:    finalizedBlock.Number,
		SignerNonceAligned: signerNonceAligned(binding, current.SignerNonce),
		SpotConfigVersion:  current.SpotConfigVersion, StockDecimals: current.StockDecimals,
		UIMultiplierE18: current.UIMultiplierE18, NewUIMultiplierE18: current.NewUIMultiplierE18,
		OraclePaused: current.OraclePaused, OracleHealthy: current.OracleHealthy,
		SequencerHealthy: current.SequencerHealthy,
		FinalizedHash:    finalizedBlock.Hash, FinalizedTimestamp: finalizedBlock.Timestamp,
		SourceBlockNumber: currentBlock.Number, SourceBlockHash: currentBlock.Hash, SourceBlockTimestamp: currentBlock.Timestamp,
		ObservedAt: time.Unix(int64(currentBlock.Timestamp), 0).UTC(),
	}, nil
}

func (c *RobinhoodClient) commonBlock(ctx context.Context, number uint64) (blockRef, error) {
	type result struct {
		block blockRef
		err   error
	}
	results := make(chan result, 2)
	for _, endpoint := range []*rpcEndpoint{c.primary, c.secondary} {
		go func(endpoint *rpcEndpoint) {
			block, err := endpoint.block(ctx, encodeQuantity(number))
			results <- result{block: block, err: err}
		}(endpoint)
	}
	first, second := <-results, <-results
	if first.err != nil {
		return blockRef{}, first.err
	}
	if second.err != nil {
		return blockRef{}, second.err
	}
	if first.block.Number != number || second.block.Number != number || first.block != second.block {
		return blockRef{}, errors.New("Robinhood RPC block disagreement")
	}
	return first.block, nil
}

func (c *RobinhoodClient) collectAt(ctx context.Context, binding RobinhoodBinding, block blockRef, includeReceipts bool) (endpointObservation, error) {
	type result struct {
		observation endpointObservation
		err         error
	}
	results := make(chan result, 2)
	for _, endpoint := range []*rpcEndpoint{c.primary, c.secondary} {
		go func(endpoint *rpcEndpoint) {
			observation, err := endpoint.collectAt(ctx, binding, block, includeReceipts)
			results <- result{observation: observation, err: err}
		}(endpoint)
	}
	first, second := <-results, <-results
	if first.err != nil {
		return endpointObservation{}, first.err
	}
	if second.err != nil {
		return endpointObservation{}, second.err
	}
	if !sameEndpointObservation(first.observation, second.observation) {
		return endpointObservation{}, errors.New("Robinhood RPC account-state disagreement")
	}
	return first.observation, nil
}

func minimumBlockNumber(blocks ...blockRef) uint64 {
	if len(blocks) == 0 {
		return 0
	}
	minimum := blocks[0].Number
	for _, block := range blocks[1:] {
		if block.Number < minimum {
			minimum = block.Number
		}
	}
	return minimum
}

func commonHeadMatches(common blockRef, heads ...blockRef) bool {
	for _, head := range heads {
		if head.Number == common.Number && head != common {
			return false
		}
	}
	return true
}

func immutableWiringMatches(observation endpointObservation, binding RobinhoodBinding) bool {
	return strings.EqualFold(observation.Owner, binding.Owner) &&
		strings.EqualFold(observation.Factory, binding.Factory) &&
		strings.EqualFold(observation.RiskManager, binding.RiskManager) &&
		strings.EqualFold(observation.SpotAdapter, binding.SpotAdapter) &&
		strings.EqualFold(observation.VaultOwner, binding.Owner) &&
		strings.EqualFold(observation.VaultRegistry, binding.Registry) &&
		strings.EqualFold(observation.VaultRiskManager, binding.RiskManager) &&
		strings.EqualFold(observation.VaultSpotAdapter, binding.SpotAdapter) &&
		strings.EqualFold(observation.VaultCodeHash, binding.VaultCodeHash)
}

func currentWiringMatches(observation endpointObservation, binding RobinhoodBinding) bool {
	if !immutableWiringMatches(observation, binding) || !validModes(observation) {
		return false
	}
	return strings.EqualFold(observation.VaultAgent, binding.Signer) ||
		agentRevoked(observation) && observation.RiskMode == 2
}

func validModes(observation endpointObservation) bool {
	return observation.GlobalMode <= 2 && observation.RiskMode <= 2
}

func agentRevoked(observation endpointObservation) bool {
	return !observation.AgentEnabled && strings.EqualFold(observation.VaultAgent, zeroAddress)
}

func signerNonceAligned(binding RobinhoodBinding, observed uint64) bool {
	return binding.SignerJournalReady && observed == binding.ExpectedSignerNonce
}

func (e *rpcEndpoint) heads(ctx context.Context, prior blockRef) (endpointHeads, error) {
	chainID, err := e.hexQuantity(ctx, "eth_chainId")
	if err != nil || chainID != mainnetChainID {
		return endpointHeads{}, errors.New("Robinhood RPC chain mismatch")
	}
	var syncing json.RawMessage
	if err := e.call(ctx, "eth_syncing", nil, &syncing); err != nil || string(syncing) != "false" {
		return endpointHeads{}, errors.New("Robinhood RPC is not synchronized")
	}
	finalized, err := e.block(ctx, "finalized")
	if err != nil {
		return endpointHeads{}, err
	}
	safe, err := e.block(ctx, "safe")
	if err != nil {
		return endpointHeads{}, err
	}
	latest, err := e.block(ctx, "latest")
	if err != nil {
		return endpointHeads{}, err
	}
	if finalized.Number == 0 || safe.Number < finalized.Number || latest.Number < safe.Number {
		return endpointHeads{}, errors.New("Robinhood RPC head ordering is invalid")
	}
	if prior.Number > 0 {
		previous, err := e.block(ctx, encodeQuantity(prior.Number))
		if err != nil || previous.Hash != prior.Hash {
			return endpointHeads{}, errors.New("Robinhood finalized chain reorg detected")
		}
	}
	return endpointHeads{Finalized: finalized, Safe: safe, Latest: latest}, nil
}

func (e *rpcEndpoint) collectAt(
	ctx context.Context,
	binding RobinhoodBinding,
	block blockRef,
	includeReceipts bool,
) (endpointObservation, error) {
	tag := encodeQuantity(block.Number)
	proof, err := e.proof(ctx, binding.Vault, tag)
	if err != nil {
		return endpointObservation{}, err
	}

	addressCalls := []struct {
		to        string
		data      string
		allowZero bool
	}{
		{binding.Registry, addressCall("2724fe09", binding.Vault), false},
		{binding.Registry, addressCall("15600884", binding.Vault), false},
		{binding.Registry, addressCall("55bbaf1e", binding.Vault), false},
		{binding.Registry, addressCall("73068297", binding.Vault), false},
		{binding.Vault, "0x8da5cb5b", false},
		{binding.Vault, "0xf5ff5c76", true},
		{binding.Vault, "0x7b103999", false},
		{binding.Vault, "0x47842663", false},
		{binding.Vault, "0x34d45c62", false},
	}
	values := make([]string, len(addressCalls))
	for index, call := range addressCalls {
		value, err := e.ethCall(ctx, call.to, call.data, tag)
		if err != nil {
			return endpointObservation{}, err
		}
		if call.allowZero {
			values[index], err = abiAddressOrZero(value)
		} else {
			values[index], err = abiAddress(value)
		}
		if err != nil {
			return endpointObservation{}, err
		}
	}
	agentEnabledRaw, err := e.ethCall(ctx, binding.Vault, "0x99d29e71", tag)
	if err != nil {
		return endpointObservation{}, err
	}
	flatRaw, err := e.ethCall(ctx, binding.Vault, "0xae37e931", tag)
	if err != nil {
		return endpointObservation{}, err
	}
	globalModeRaw, err := e.ethCall(ctx, binding.Registry, "0x5c3569a2", tag)
	if err != nil {
		return endpointObservation{}, err
	}
	riskModeRaw, err := e.ethCall(ctx, binding.RiskManager, "0x295a5212", tag)
	if err != nil {
		return endpointObservation{}, err
	}
	market, err := e.ethCallWords(ctx, binding.RiskManager, addressCall("8e8f294b", aaplAddress), tag, 10)
	if err != nil {
		return endpointObservation{}, err
	}
	spotConfigVersion := abiUint(market[4])
	if spotConfigVersion == 0 {
		return endpointObservation{}, errors.New("Robinhood AAPL market is not configured")
	}
	marketFeed, err := abiAddress(market[0])
	if err != nil {
		return endpointObservation{}, errors.New("Robinhood AAPL market feed is invalid")
	}
	heartbeat := abiUint(market[3])
	oracleRound, err := e.ethCallWords(ctx, marketFeed, "0xfeaf968c", tag, 5)
	if err != nil {
		return endpointObservation{}, err
	}
	oracleHealthy := oracleRoundHealthy(oracleRound, block.Timestamp, heartbeat)
	sequencerFeedRaw, err := e.ethCall(ctx, binding.RiskManager, "0x3b521cb6", tag)
	if err != nil {
		return endpointObservation{}, err
	}
	sequencerFeed, err := abiAddress(sequencerFeedRaw)
	if err != nil {
		return endpointObservation{}, errors.New("Robinhood sequencer feed is invalid")
	}
	sequencerRound, err := e.ethCallWords(ctx, sequencerFeed, "0xfeaf968c", tag, 5)
	if err != nil {
		return endpointObservation{}, err
	}
	graceRaw, err := e.ethCall(ctx, binding.RiskManager, "0x26a97b94", tag)
	if err != nil {
		return endpointObservation{}, err
	}
	sequencerHealthy := sequencerRoundHealthy(sequencerRound, block.Timestamp, abiUint(graceRaw))
	stockDecimalsRaw, err := e.ethCall(ctx, aaplAddress, "0x313ce567", tag)
	if err != nil {
		return endpointObservation{}, err
	}
	stockDecimals := abiUint(stockDecimalsRaw)
	if stockDecimals > 18 {
		return endpointObservation{}, errors.New("Robinhood AAPL decimals are invalid")
	}
	uiMultiplierRaw, err := e.ethCall(ctx, aaplAddress, "0xa60bf13d", tag)
	if err != nil {
		return endpointObservation{}, err
	}
	newUIMultiplierRaw, err := e.ethCall(ctx, aaplAddress, "0xdc767007", tag)
	if err != nil {
		return endpointObservation{}, err
	}
	oraclePausedRaw, err := e.ethCall(ctx, aaplAddress, "0x7706ba52", tag)
	if err != nil {
		return endpointObservation{}, err
	}
	settlementRaw, err := e.ethCall(ctx, usdgAddress, addressCall("70a08231", binding.Vault), tag)
	if err != nil {
		return endpointObservation{}, err
	}
	stockRaw, err := e.ethCall(ctx, aaplAddress, addressCall("70a08231", binding.Vault), tag)
	if err != nil {
		return endpointObservation{}, err
	}
	ownerGas, err := e.balance(ctx, binding.Owner, tag)
	if err != nil {
		return endpointObservation{}, err
	}
	signerGas, err := e.balance(ctx, binding.Signer, tag)
	if err != nil {
		return endpointObservation{}, err
	}
	signerNonce, err := e.transactionCount(ctx, binding.Signer, tag)
	if err != nil {
		return endpointObservation{}, err
	}
	var receipts []rpcReceipt
	if includeReceipts {
		receipts = make([]rpcReceipt, 0, len(binding.ReceiptHashes))
		for _, hash := range binding.ReceiptHashes {
			var receipt rpcReceipt
			if err := e.call(ctx, "eth_getTransactionReceipt", []interface{}{hash}, &receipt); err != nil {
				return endpointObservation{}, err
			}
			if !strings.EqualFold(receipt.TransactionHash, hash) {
				return endpointObservation{}, errors.New("Robinhood receipt hash mismatch")
			}
			receiptBlock, err := e.block(ctx, receipt.BlockNumber)
			if err != nil || !strings.EqualFold(receiptBlock.Hash, receipt.BlockHash) {
				return endpointObservation{}, errors.New("Robinhood receipt block mismatch")
			}
			receipts = append(receipts, receipt)
		}
	}
	return endpointObservation{
		Block: block, Owner: values[0], Factory: values[1], RiskManager: values[2], SpotAdapter: values[3],
		VaultOwner: values[4], VaultAgent: values[5], VaultRegistry: values[6], VaultRiskManager: values[7], VaultSpotAdapter: values[8],
		VaultCodeHash: strings.ToLower(proof.CodeHash), AgentEnabled: abiBool(agentEnabledRaw), Flat: abiBool(flatRaw),
		GlobalMode: abiUint(globalModeRaw), RiskMode: abiUint(riskModeRaw), SettlementBalanceRaw: abiUintString(settlementRaw),
		StockBalanceRaw: abiUintString(stockRaw),
		OwnerGasRaw:     ownerGas, SignerGasRaw: signerGas, Receipts: receipts,
		SignerNonce: signerNonce, SpotConfigVersion: spotConfigVersion, StockDecimals: uint8(stockDecimals),
		UIMultiplierE18: abiUintString(uiMultiplierRaw), NewUIMultiplierE18: abiUintString(newUIMultiplierRaw),
		OraclePaused: abiBool(oraclePausedRaw), OracleHealthy: oracleHealthy, SequencerHealthy: sequencerHealthy,
	}, nil
}

func staleBlock(block blockRef, now time.Time, maximumAge time.Duration) bool {
	if block.Number == 0 || block.Timestamp == 0 || now.Unix() < 0 || block.Timestamp > uint64(now.Unix()) {
		return true
	}
	observed := time.Unix(int64(block.Timestamp), 0)
	age := now.Sub(observed)
	return age < 0 || age > maximumAge
}

func (e *rpcEndpoint) call(ctx context.Context, method string, params []interface{}, target interface{}) error {
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusMethodNotAllowed {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("Robinhood RPC returned status %d", resp.StatusCode)
	}
	var envelope rpcResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&envelope); err != nil || envelope.Error != nil || envelope.ID != 1 {
		return errors.New("invalid Robinhood RPC response")
	}
	if string(envelope.Result) == "null" || len(envelope.Result) == 0 {
		return errors.New("Robinhood RPC returned no result")
	}
	return json.Unmarshal(envelope.Result, target)
}

func (e *rpcEndpoint) block(ctx context.Context, tag string) (blockRef, error) {
	var block rpcBlock
	if err := e.call(ctx, "eth_getBlockByNumber", []interface{}{tag, false}, &block); err != nil {
		return blockRef{}, err
	}
	number, err := parseQuantity(block.Number)
	timestamp, timestampErr := parseQuantity(block.Timestamp)
	if err != nil || timestampErr != nil || timestamp == 0 || !validHash(block.Hash) {
		return blockRef{}, errors.New("invalid Robinhood block")
	}
	return blockRef{Number: number, Hash: strings.ToLower(block.Hash), Timestamp: timestamp}, nil
}

func (e *rpcEndpoint) proof(ctx context.Context, address, tag string) (rpcProof, error) {
	var proof rpcProof
	if err := e.call(ctx, "eth_getProof", []interface{}{address, []string{}, tag}, &proof); err != nil {
		return rpcProof{}, err
	}
	if !strings.EqualFold(proof.Address, address) || !validHash(proof.CodeHash) {
		return rpcProof{}, errors.New("Robinhood proof account mismatch")
	}
	return proof, nil
}

func (e *rpcEndpoint) ethCall(ctx context.Context, to, data, tag string) (string, error) {
	var result string
	err := e.call(ctx, "eth_call", []interface{}{map[string]string{"to": to, "data": data}, tag}, &result)
	if err != nil || len(result) != 66 || !strings.HasPrefix(result, "0x") {
		return "", errors.New("invalid Robinhood contract response")
	}
	return result, nil
}

func (e *rpcEndpoint) ethCallWords(ctx context.Context, to, data, tag string, count int) ([]string, error) {
	var result string
	err := e.call(ctx, "eth_call", []interface{}{map[string]string{"to": to, "data": data}, tag}, &result)
	if err != nil || count <= 0 || !strings.HasPrefix(result, "0x") || len(result) != 2+64*count {
		return nil, errors.New("invalid Robinhood contract response")
	}
	words := make([]string, count)
	for index := range words {
		words[index] = "0x" + result[2+64*index:2+64*(index+1)]
	}
	return words, nil
}

func (e *rpcEndpoint) balance(ctx context.Context, address, tag string) (string, error) {
	var result string
	if err := e.call(ctx, "eth_getBalance", []interface{}{address, tag}, &result); err != nil {
		return "", err
	}
	value, err := parseQuantityBig(result)
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

func (e *rpcEndpoint) transactionCount(ctx context.Context, address, tag string) (uint64, error) {
	var result string
	if err := e.call(ctx, "eth_getTransactionCount", []interface{}{address, tag}, &result); err != nil {
		return 0, err
	}
	return parseQuantity(result)
}

func (e *rpcEndpoint) hexQuantity(ctx context.Context, method string) (uint64, error) {
	var result string
	if err := e.call(ctx, method, nil, &result); err != nil {
		return 0, err
	}
	return parseQuantity(result)
}

func validateRobinhoodBinding(binding RobinhoodBinding) error {
	for _, address := range []string{binding.Registry, binding.Factory, binding.Vault, binding.RiskManager, binding.SpotAdapter, binding.Owner, binding.Signer} {
		if !validAddress(strings.ToLower(address)) {
			return errors.New("invalid Robinhood binding")
		}
	}
	if !validHash(binding.VaultCodeHash) || !decimalAtLeast(binding.MinimumSettlementRaw, "25000000") ||
		!decimalAtLeast(binding.MinimumOwnerGasRaw, "1") || !decimalAtLeast(binding.MinimumSignerGasRaw, "1") ||
		len(binding.ReceiptHashes) < 2 || !binding.SignerJournalReady {
		return errors.New("unsafe Robinhood minimums")
	}
	seen := make(map[string]struct{}, len(binding.ReceiptHashes))
	for _, hash := range binding.ReceiptHashes {
		if !validHash(hash) {
			return errors.New("invalid receipt hash")
		}
		hash = strings.ToLower(hash)
		if _, exists := seen[hash]; exists {
			return errors.New("duplicate receipt hash")
		}
		seen[hash] = struct{}{}
	}
	return nil
}

func sameEndpointObservation(left, right endpointObservation) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Equal(leftJSON, rightJSON)
}

func receiptsFinal(receipts []rpcReceipt, finalized blockRef) bool {
	for _, receipt := range receipts {
		number, err := parseQuantity(receipt.BlockNumber)
		if err != nil || number > finalized.Number || receipt.Status != "0x1" || !validHash(receipt.BlockHash) {
			return false
		}
	}
	return true
}

func receiptsBound(receipts []rpcReceipt, binding RobinhoodBinding) bool {
	allowed := map[string]struct{}{
		strings.ToLower(binding.Registry): {}, strings.ToLower(binding.Factory): {}, strings.ToLower(binding.Vault): {},
		strings.ToLower(binding.RiskManager): {}, strings.ToLower(binding.SpotAdapter): {}, usdgAddress: {}, aaplAddress: {},
	}
	for _, receipt := range receipts {
		bound := false
		for _, address := range append([]string{receipt.To, receipt.ContractAddress}, logAddresses(receipt.Logs)...) {
			if _, ok := allowed[strings.ToLower(address)]; ok {
				bound = true
				break
			}
		}
		if !bound {
			return false
		}
	}
	return true
}

func logAddresses(logs []struct {
	Address string `json:"address"`
}) []string {
	addresses := make([]string, 0, len(logs))
	for _, log := range logs {
		addresses = append(addresses, log.Address)
	}
	return addresses
}

func addressCall(selector, address string) string {
	return "0x" + selector + strings.Repeat("0", 24) + strings.ToLower(address[2:])
}

func abiAddress(value string) (string, error) {
	address, err := abiAddressOrZero(value)
	if err != nil || address == zeroAddress {
		return "", errors.New("invalid ABI address")
	}
	return address, nil
}

func abiAddressOrZero(value string) (string, error) {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") {
		return "", errors.New("invalid ABI address")
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil || len(decoded) != 32 {
		return "", errors.New("invalid ABI address")
	}
	address := "0x" + hex.EncodeToString(decoded[12:])
	if address != zeroAddress && !validAddress(address) {
		return "", errors.New("invalid ABI address")
	}
	return address, nil
}

func abiBool(value string) bool { return abiUint(value) == 1 }

func abiUint(value string) uint64 {
	parsed, err := strconv.ParseUint(strings.TrimLeft(value[2:], "0"), 16, 64)
	if err != nil && strings.TrimLeft(value[2:], "0") != "" {
		return ^uint64(0)
	}
	return parsed
}

func abiUintString(value string) string {
	parsed := new(big.Int)
	if _, ok := parsed.SetString(value[2:], 16); !ok {
		return ""
	}
	return parsed.String()
}

func roundIdentityHealthy(words []string, blockTimestamp uint64) bool {
	if len(words) != 5 || blockTimestamp == 0 {
		return false
	}
	roundID := abiUint(words[0])
	startedAt := abiUint(words[2])
	updatedAt := abiUint(words[3])
	answeredInRound := abiUint(words[4])
	return roundID > 0 && startedAt > 0 && updatedAt > 0 && startedAt <= blockTimestamp &&
		updatedAt <= blockTimestamp && answeredInRound >= roundID
}

func oracleRoundHealthy(words []string, blockTimestamp, heartbeat uint64) bool {
	if !roundIdentityHealthy(words, blockTimestamp) || heartbeat == 0 || blockTimestamp-abiUint(words[3]) > heartbeat {
		return false
	}
	answer := new(big.Int)
	_, ok := answer.SetString(words[1][2:], 16)
	return ok && answer.Sign() > 0 && answer.Bit(255) == 0
}

func sequencerRoundHealthy(words []string, blockTimestamp, grace uint64) bool {
	if !roundIdentityHealthy(words, blockTimestamp) || grace == 0 || blockTimestamp-abiUint(words[2]) <= grace {
		return false
	}
	return abiUint(words[1]) == 0
}

func parseQuantity(value string) (uint64, error) {
	parsed, err := parseQuantityBig(value)
	if err != nil || !parsed.IsUint64() {
		return 0, errors.New("invalid RPC quantity")
	}
	return parsed.Uint64(), nil
}

func parseQuantityBig(value string) (*big.Int, error) {
	if !strings.HasPrefix(value, "0x") || len(value) < 3 {
		return nil, errors.New("invalid RPC quantity")
	}
	parsed := new(big.Int)
	if _, ok := parsed.SetString(value[2:], 16); !ok || parsed.Sign() < 0 {
		return nil, errors.New("invalid RPC quantity")
	}
	return parsed, nil
}

func encodeQuantity(value uint64) string { return fmt.Sprintf("0x%x", value) }

func validHash(value string) bool {
	return len(value) == 66 && strings.HasPrefix(value, "0x") && func() bool {
		_, err := hex.DecodeString(value[2:])
		return err == nil && value != "0x"+strings.Repeat("0", 64)
	}()
}

func modeName(value uint64) string {
	switch value {
	case 0:
		return "ACTIVE"
	case 1:
		return "REDUCE_ONLY"
	default:
		return "HALTED"
	}
}
