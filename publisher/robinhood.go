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
	usdgAddress = "0x5fc5360d0400a0fd4f2af552add042d716f1d168"
	aaplAddress = "0xaf3d76f1834a1d425780943c99ea8a608f8a93f9"
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
	Number uint64 `json:"number"`
	Hash   string `json:"hash"`
}

type rpcBlock struct {
	Number string `json:"number"`
	Hash   string `json:"hash"`
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
	Finalized            blockRef
	Safe                 blockRef
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
	Receipts             []rpcReceipt
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
	receiptHashes, err := loadReceiptJournal(binding)
	if err != nil {
		return RobinhoodObservation{}, err
	}
	binding.ReceiptHashes = receiptHashes
	c.mu.Lock()
	prior := c.finalized[binding.Vault]
	c.mu.Unlock()

	type result struct {
		observation endpointObservation
		err         error
	}
	results := make(chan result, 2)
	for _, endpoint := range []*rpcEndpoint{c.primary, c.secondary} {
		go func(endpoint *rpcEndpoint) {
			observation, err := endpoint.collect(ctx, binding, prior)
			results <- result{observation: observation, err: err}
		}(endpoint)
	}
	first, second := <-results, <-results
	if first.err != nil {
		return RobinhoodObservation{}, first.err
	}
	if second.err != nil {
		return RobinhoodObservation{}, second.err
	}
	primary, secondary := first.observation, second.observation
	if !sameEndpointObservation(primary, secondary) {
		return RobinhoodObservation{Vault: binding.Vault, Signer: binding.Signer, Owner: binding.Owner, ObservedAt: time.Now().UTC()}, nil
	}

	wiring := strings.EqualFold(primary.Owner, binding.Owner) && strings.EqualFold(primary.Factory, binding.Factory) &&
		strings.EqualFold(primary.RiskManager, binding.RiskManager) && strings.EqualFold(primary.SpotAdapter, binding.SpotAdapter) &&
		strings.EqualFold(primary.VaultOwner, binding.Owner) && strings.EqualFold(primary.VaultAgent, binding.Signer) &&
		strings.EqualFold(primary.VaultRegistry, binding.Registry) && strings.EqualFold(primary.VaultRiskManager, binding.RiskManager) &&
		strings.EqualFold(primary.VaultSpotAdapter, binding.SpotAdapter) && strings.EqualFold(primary.VaultCodeHash, binding.VaultCodeHash) &&
		primary.GlobalMode <= 2 && primary.RiskMode <= 2
	finality := primary.Finalized.Number > 0 && primary.Safe.Number >= primary.Finalized.Number &&
		receiptsFinal(primary.Receipts, primary.Finalized) && receiptsBound(primary.Receipts, binding)
	if prior.Number > 0 && primary.Finalized.Number < prior.Number {
		finality = false
	}
	if finality {
		c.mu.Lock()
		c.finalized[binding.Vault] = primary.Finalized
		c.mu.Unlock()
	}
	return RobinhoodObservation{
		Vault: binding.Vault, Signer: binding.Signer, Owner: binding.Owner,
		SettlementBalanceRaw: primary.SettlementBalanceRaw, OwnerGasRaw: primary.OwnerGasRaw, SignerGasRaw: primary.SignerGasRaw,
		AgentEnabled: primary.AgentEnabled, Flat: primary.Flat && primary.StockBalanceRaw == "0", WiringVerified: wiring, FinalityHealthy: finality,
		FundingReady:   decimalAtLeast(primary.SettlementBalanceRaw, binding.MinimumSettlementRaw),
		OwnerGasReady:  decimalAtLeast(primary.OwnerGasRaw, binding.MinimumOwnerGasRaw),
		SignerGasReady: decimalAtLeast(primary.SignerGasRaw, binding.MinimumSignerGasRaw),
		RiskMode:       modeName(primary.RiskMode), FinalizedNumber: primary.Finalized.Number,
		FinalizedHash: primary.Finalized.Hash, ObservedAt: time.Now().UTC(),
	}, nil
}

func (e *rpcEndpoint) collect(ctx context.Context, binding RobinhoodBinding, prior blockRef) (endpointObservation, error) {
	chainID, err := e.hexQuantity(ctx, "eth_chainId")
	if err != nil || chainID != mainnetChainID {
		return endpointObservation{}, errors.New("Robinhood RPC chain mismatch")
	}
	var syncing json.RawMessage
	if err := e.call(ctx, "eth_syncing", nil, &syncing); err != nil || string(syncing) != "false" {
		return endpointObservation{}, errors.New("Robinhood RPC is not synchronized")
	}
	finalized, err := e.block(ctx, "finalized")
	if err != nil {
		return endpointObservation{}, err
	}
	safe, err := e.block(ctx, "safe")
	if err != nil {
		return endpointObservation{}, err
	}
	if prior.Number > 0 {
		previous, err := e.block(ctx, encodeQuantity(prior.Number))
		if err != nil || previous.Hash != prior.Hash {
			return endpointObservation{}, errors.New("Robinhood finalized chain reorg detected")
		}
	}
	tag := encodeQuantity(finalized.Number)
	proof, err := e.proof(ctx, binding.Vault, tag)
	if err != nil {
		return endpointObservation{}, err
	}

	addressCalls := []struct {
		to   string
		data string
		dest *string
	}{
		{binding.Registry, addressCall("2724fe09", binding.Vault), nil},
		{binding.Registry, addressCall("15600884", binding.Vault), nil},
		{binding.Registry, addressCall("55bbaf1e", binding.Vault), nil},
		{binding.Registry, addressCall("73068297", binding.Vault), nil},
		{binding.Vault, "0x8da5cb5b", nil},
		{binding.Vault, "0xf5ff5c76", nil},
		{binding.Vault, "0x7b103999", nil},
		{binding.Vault, "0x47842663", nil},
		{binding.Vault, "0x34d45c62", nil},
	}
	values := make([]string, len(addressCalls))
	for index, call := range addressCalls {
		value, err := e.ethCall(ctx, call.to, call.data, tag)
		if err != nil {
			return endpointObservation{}, err
		}
		values[index], err = abiAddress(value)
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
	receipts := make([]rpcReceipt, 0, len(binding.ReceiptHashes))
	for _, hash := range binding.ReceiptHashes {
		var receipt rpcReceipt
		if err := e.call(ctx, "eth_getTransactionReceipt", []interface{}{hash}, &receipt); err != nil {
			return endpointObservation{}, err
		}
		if !strings.EqualFold(receipt.TransactionHash, hash) {
			return endpointObservation{}, errors.New("Robinhood receipt hash mismatch")
		}
		block, err := e.block(ctx, receipt.BlockNumber)
		if err != nil || !strings.EqualFold(block.Hash, receipt.BlockHash) {
			return endpointObservation{}, errors.New("Robinhood receipt block mismatch")
		}
		receipts = append(receipts, receipt)
	}
	return endpointObservation{
		Finalized: finalized, Safe: safe, Owner: values[0], Factory: values[1], RiskManager: values[2], SpotAdapter: values[3],
		VaultOwner: values[4], VaultAgent: values[5], VaultRegistry: values[6], VaultRiskManager: values[7], VaultSpotAdapter: values[8],
		VaultCodeHash: strings.ToLower(proof.CodeHash), AgentEnabled: abiBool(agentEnabledRaw), Flat: abiBool(flatRaw),
		GlobalMode: abiUint(globalModeRaw), RiskMode: abiUint(riskModeRaw), SettlementBalanceRaw: abiUintString(settlementRaw),
		StockBalanceRaw: abiUintString(stockRaw),
		OwnerGasRaw:     ownerGas, SignerGasRaw: signerGas, Receipts: receipts,
	}, nil
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
	if err != nil || !validHash(block.Hash) {
		return blockRef{}, errors.New("invalid Robinhood block")
	}
	return blockRef{Number: number, Hash: strings.ToLower(block.Hash)}, nil
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
		binding.ReceiptJournalFile == "" {
		return errors.New("unsafe Robinhood minimums")
	}
	for _, hash := range binding.ReceiptHashes {
		if !validHash(hash) {
			return errors.New("invalid receipt hash")
		}
	}
	return nil
}

type receiptJournal struct {
	Vault  string   `json:"vault"`
	Hashes []string `json:"hashes"`
}

func loadReceiptJournal(binding RobinhoodBinding) ([]string, error) {
	encoded, err := readSecretFile(binding.ReceiptJournalFile)
	if err != nil {
		return nil, fmt.Errorf("read receipt journal: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var journal receiptJournal
	if err := decoder.Decode(&journal); err != nil || !strings.EqualFold(journal.Vault, binding.Vault) {
		return nil, errors.New("receipt journal binding mismatch")
	}
	if len(journal.Hashes) == 0 {
		return nil, errors.New("receipt journal is empty")
	}
	seen := make(map[string]struct{}, len(journal.Hashes))
	for index, hash := range journal.Hashes {
		hash = strings.ToLower(hash)
		if !validHash(hash) {
			return nil, errors.New("invalid receipt hash")
		}
		if _, exists := seen[hash]; exists {
			return nil, errors.New("duplicate receipt hash")
		}
		seen[hash] = struct{}{}
		journal.Hashes[index] = hash
	}
	return journal.Hashes, nil
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
	decoded, err := hex.DecodeString(value[2:])
	if err != nil || len(decoded) != 32 {
		return "", errors.New("invalid ABI address")
	}
	address := "0x" + hex.EncodeToString(decoded[12:])
	if !validAddress(address) {
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
