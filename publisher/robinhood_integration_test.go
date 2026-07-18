package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRobinhoodDualRPCCollection(t *testing.T) {
	binding := robinhoodTestBinding(t)
	primary := robinhoodRPCServer(t, binding, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	secondary := robinhoodRPCServer(t, binding, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	defer primary.Close()
	defer secondary.Close()
	client := &RobinhoodClient{
		primary:   &rpcEndpoint{url: primary.URL, host: "provider-one", client: primary.Client()},
		secondary: &rpcEndpoint{url: secondary.URL, host: "provider-two", client: secondary.Client()},
		finalized: make(map[string]blockRef),
	}
	observation, err := client.Collect(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.WiringVerified || !observation.FinalityHealthy || !observation.FundingReady ||
		!observation.OwnerGasReady || !observation.SignerGasReady || !observation.AgentEnabled || !observation.Flat ||
		!observation.FinalizedAgentEnabled || observation.FinalizedAgentRevoked ||
		observation.FinalizedAgentAddress != binding.Signer ||
		observation.GlobalMode != "ACTIVE" || observation.FinalizedGlobalMode != "ACTIVE" ||
		observation.FinalizedRiskMode != "ACTIVE" || !observation.SignerNonceAligned ||
		!observation.OracleHealthy || !observation.SequencerHealthy {
		t.Fatalf("unexpected observation: %+v", observation)
	}
}

func TestRobinhoodDualRPCRejectsUnfinalizedAuthorizationReceipt(t *testing.T) {
	binding := robinhoodTestBinding(t)
	now := time.Now().UTC().Truncate(time.Second)
	finalized := blockRef{
		Number: 100, Hash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Timestamp: uint64(now.Add(-15 * time.Minute).Unix()),
	}
	authorization := blockRef{
		Number: 101, Hash: "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Timestamp: uint64(now.Add(-14 * time.Minute).Unix()),
	}
	current := blockRef{
		Number: 110, Hash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Timestamp: uint64(now.Unix()),
	}
	config := robinhoodRPCConfig{
		Finalized: finalized, Safe: current, Latest: current,
		Blocks:       map[uint64]blockRef{100: finalized, 101: authorization, 110: current},
		ReceiptBlock: finalized,
		ReceiptBlocks: map[string]blockRef{
			binding.ReceiptHashes[1]: authorization,
		},
	}
	primary := robinhoodRPCServerWithConfig(t, binding, config)
	secondary := robinhoodRPCServerWithConfig(t, binding, config)
	defer primary.Close()
	defer secondary.Close()

	observation, err := robinhoodTestClient(primary, secondary).Collect(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if observation.FinalityHealthy || observation.Healthy() {
		t.Fatalf("unfinalized authorization receipt was accepted: %+v", observation)
	}
}

func TestRobinhoodDualRPCPropagatesGlobalRestriction(t *testing.T) {
	binding := robinhoodTestBinding(t)
	now := time.Now().UTC().Truncate(time.Second)
	finalized := blockRef{
		Number: 100, Hash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Timestamp: uint64(now.Add(-15 * time.Minute).Unix()),
	}
	current := blockRef{
		Number: 110, Hash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Timestamp: uint64(now.Unix()),
	}
	config := robinhoodRPCConfig{
		Finalized: finalized, Safe: current, Latest: current,
		Blocks:       map[uint64]blockRef{100: finalized, 110: current},
		ReceiptBlock: finalized,
		StateByBlock: map[uint64]robinhoodBlockState{
			100: {VaultAgent: binding.Signer, AgentEnabled: true},
			110: {VaultAgent: binding.Signer, AgentEnabled: true, GlobalMode: 1},
		},
	}
	primary := robinhoodRPCServerWithConfig(t, binding, config)
	secondary := robinhoodRPCServerWithConfig(t, binding, config)
	defer primary.Close()
	defer secondary.Close()

	observation, err := robinhoodTestClient(primary, secondary).Collect(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if observation.GlobalMode != "REDUCE_ONLY" || observation.FinalizedGlobalMode != "ACTIVE" ||
		observation.Healthy() {
		t.Fatalf("global restriction was not propagated: %+v", observation)
	}
}

func TestRobinhoodDualRPCUsesFreshCurrentStateWithRealisticFinalityLag(t *testing.T) {
	binding := robinhoodTestBinding(t)
	now := time.Now().UTC().Truncate(time.Second)
	finalized := blockRef{
		Number: 100, Hash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Timestamp: uint64(now.Add(-15 * time.Minute).Unix()),
	}
	current := blockRef{
		Number: 110, Hash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Timestamp: uint64(now.Unix()),
	}
	safe := blockRef{
		Number: 105, Hash: "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Timestamp: uint64(now.Add(-8 * time.Minute).Unix()),
	}
	config := robinhoodRPCConfig{
		Finalized: finalized, Safe: safe, Latest: current,
		Blocks:            map[uint64]blockRef{100: finalized, 105: safe, 110: current},
		ReceiptBlock:      finalized,
		SettlementByBlock: map[uint64]uint64{100: 0, 110: 50_000_000},
	}
	primary := robinhoodRPCServerWithConfig(t, binding, config)
	secondary := robinhoodRPCServerWithConfig(t, binding, config)
	defer primary.Close()
	defer secondary.Close()
	client := robinhoodTestClient(primary, secondary)
	observation, err := client.Collect(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Healthy() || observation.FinalizedNumber != finalized.Number ||
		observation.FinalizedHash != finalized.Hash || observation.FinalizedTimestamp != finalized.Timestamp ||
		observation.SourceBlockNumber != current.Number || observation.SourceBlockHash != current.Hash ||
		observation.SourceBlockTimestamp != current.Timestamp || !observation.ObservedAt.Equal(now) {
		t.Fatalf("finalized and mutable evidence were not separated: %+v", observation)
	}
}

func TestRobinhoodDualRPCSeparatesLatestRevocationFromFinalizedProof(t *testing.T) {
	binding := robinhoodTestBinding(t)
	now := time.Now().UTC().Truncate(time.Second)
	finalized := blockRef{
		Number: 100, Hash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Timestamp: uint64(now.Add(-15 * time.Minute).Unix()),
	}
	current := blockRef{
		Number: 110, Hash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Timestamp: uint64(now.Unix()),
	}
	safe := blockRef{
		Number: 105, Hash: "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Timestamp: uint64(now.Add(-8 * time.Minute).Unix()),
	}
	config := robinhoodRPCConfig{
		Finalized: finalized, Safe: safe, Latest: current,
		Blocks:       map[uint64]blockRef{100: finalized, 105: safe, 110: current},
		ReceiptBlock: finalized,
		StateByBlock: map[uint64]robinhoodBlockState{
			100: {VaultAgent: binding.Signer, AgentEnabled: true, RiskMode: 0},
			110: {VaultAgent: zeroAddress, AgentEnabled: false, RiskMode: 2},
		},
	}
	primary := robinhoodRPCServerWithConfig(t, binding, config)
	secondary := robinhoodRPCServerWithConfig(t, binding, config)
	defer primary.Close()
	defer secondary.Close()

	observation, err := robinhoodTestClient(primary, secondary).Collect(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if observation.AgentEnabled || observation.RiskMode != "HALTED" ||
		!observation.FinalizedAgentEnabled || observation.FinalizedAgentRevoked ||
		observation.FinalizedRiskMode != "ACTIVE" || !observation.WiringVerified {
		t.Fatalf("latest revocation was conflated with finality: %+v", observation)
	}

	finalizedConfig := robinhoodRPCConfig{
		Finalized: current, Safe: current, Latest: current,
		Blocks:       map[uint64]blockRef{110: current},
		ReceiptBlock: current,
		StateByBlock: map[uint64]robinhoodBlockState{
			110: {VaultAgent: zeroAddress, AgentEnabled: false, RiskMode: 2},
		},
	}
	finalizedPrimary := robinhoodRPCServerWithConfig(t, binding, finalizedConfig)
	finalizedSecondary := robinhoodRPCServerWithConfig(t, binding, finalizedConfig)
	defer finalizedPrimary.Close()
	defer finalizedSecondary.Close()
	finalizedObservation, err := robinhoodTestClient(finalizedPrimary, finalizedSecondary).
		Collect(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if finalizedObservation.FinalizedAgentEnabled ||
		!finalizedObservation.FinalizedAgentRevoked ||
		finalizedObservation.FinalizedRiskMode != "HALTED" ||
		!finalizedObservation.WiringVerified {
		t.Fatalf("finalized revocation proof was not published: %+v", finalizedObservation)
	}
}

func TestRobinhoodDualRPCReconcilesProviderSkewAtCommonLatestHead(t *testing.T) {
	binding := robinhoodTestBinding(t)
	now := time.Now().UTC().Truncate(time.Second)
	finalized := blockRef{
		Number: 100, Hash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Timestamp: uint64(now.Add(-15 * time.Minute).Unix()),
	}
	primaryFinalized := blockRef{
		Number: 101, Hash: "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Timestamp: uint64(now.Add(-14 * time.Minute).Unix()),
	}
	commonCurrent := blockRef{
		Number: 110, Hash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Timestamp: uint64(now.Unix()),
	}
	primarySafe := blockRef{
		Number: 106, Hash: "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Timestamp: uint64(now.Add(-8 * time.Minute).Unix()),
	}
	primaryLatest := blockRef{
		Number: 112, Hash: "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Timestamp: uint64(now.Unix()),
	}
	secondarySafe := blockRef{
		Number: 105, Hash: "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		Timestamp: uint64(now.Add(-8*time.Minute - 30*time.Second).Unix()),
	}
	primary := robinhoodRPCServerWithConfig(t, binding, robinhoodRPCConfig{
		Finalized: primaryFinalized, Safe: primarySafe, Latest: primaryLatest,
		Blocks: map[uint64]blockRef{
			100: finalized, 101: primaryFinalized, 105: secondarySafe, 106: primarySafe,
			110: commonCurrent, 112: primaryLatest,
		},
		ReceiptBlock: finalized,
	})
	secondary := robinhoodRPCServerWithConfig(t, binding, robinhoodRPCConfig{
		Finalized: finalized, Safe: secondarySafe, Latest: commonCurrent,
		Blocks:       map[uint64]blockRef{100: finalized, 105: secondarySafe, 110: commonCurrent},
		ReceiptBlock: finalized,
	})
	defer primary.Close()
	defer secondary.Close()
	client := robinhoodTestClient(primary, secondary)
	observation, err := client.Collect(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Healthy() || observation.FinalizedNumber != finalized.Number ||
		observation.SourceBlockNumber != commonCurrent.Number || observation.SourceBlockHash != commonCurrent.Hash {
		t.Fatalf("skewed healthy RPCs did not reconcile at common latest block: %+v", observation)
	}
}

func TestRobinhoodDualRPCRejectsCurrentBlockDisagreement(t *testing.T) {
	binding := robinhoodTestBinding(t)
	now := time.Now().UTC().Truncate(time.Second)
	finalized := blockRef{
		Number: 100, Hash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Timestamp: uint64(now.Add(-15 * time.Minute).Unix()),
	}
	firstCurrent := blockRef{
		Number: 110, Hash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Timestamp: uint64(now.Unix()),
	}
	secondCurrent := firstCurrent
	secondCurrent.Hash = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	primary := robinhoodRPCServerWithConfig(t, binding, robinhoodRPCConfig{
		Finalized: finalized, Safe: firstCurrent, Latest: firstCurrent,
		Blocks: map[uint64]blockRef{100: finalized, 110: firstCurrent}, ReceiptBlock: finalized,
	})
	secondary := robinhoodRPCServerWithConfig(t, binding, robinhoodRPCConfig{
		Finalized: finalized, Safe: secondCurrent, Latest: secondCurrent,
		Blocks: map[uint64]blockRef{100: finalized, 110: secondCurrent}, ReceiptBlock: finalized,
	})
	defer primary.Close()
	defer secondary.Close()
	observation, err := robinhoodTestClient(primary, secondary).Collect(context.Background(), binding)
	if err == nil || observation.Healthy() {
		t.Fatalf("current RPC disagreement must fail closed: observation=%+v err=%v", observation, err)
	}
}

func TestRobinhoodDualRPCRejectsStaleCurrentBlock(t *testing.T) {
	binding := robinhoodTestBinding(t)
	now := time.Now().UTC().Truncate(time.Second)
	finalized := blockRef{
		Number: 100, Hash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Timestamp: uint64(now.Add(-15 * time.Minute).Unix()),
	}
	stale := blockRef{
		Number: 110, Hash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Timestamp: uint64(now.Add(-6 * time.Second).Unix()),
	}
	config := robinhoodRPCConfig{
		Finalized: finalized, Safe: stale, Latest: stale,
		Blocks: map[uint64]blockRef{100: finalized, 110: stale}, ReceiptBlock: finalized,
	}
	primary := robinhoodRPCServerWithConfig(t, binding, config)
	secondary := robinhoodRPCServerWithConfig(t, binding, config)
	defer primary.Close()
	defer secondary.Close()
	observation, err := robinhoodTestClient(primary, secondary).Collect(context.Background(), binding)
	if err == nil || observation.Healthy() {
		t.Fatalf("stale current state must fail closed: observation=%+v err=%v", observation, err)
	}
}

func robinhoodTestBinding(t *testing.T) RobinhoodBinding {
	t.Helper()
	return RobinhoodBinding{
		Registry: "0x6666666666666666666666666666666666666666", Factory: "0x2222222222222222222222222222222222222222",
		Vault: "0x9999999999999999999999999999999999999999", RiskManager: "0x3333333333333333333333333333333333333333",
		SpotAdapter: "0x4444444444444444444444444444444444444444", Owner: "0x1111111111111111111111111111111111111111",
		Signer:               "0x5555555555555555555555555555555555555555",
		VaultCodeHash:        "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		MinimumSettlementRaw: "25000000", MinimumOwnerGasRaw: "1", MinimumSignerGasRaw: "1",
		ReceiptHashes: []string{
			"0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			"0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		},
		ExpectedSignerNonce: 7, SignerJournalReady: true,
	}
}

func robinhoodRPCServer(t *testing.T, binding RobinhoodBinding, finalizedHash string) *httptest.Server {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	finalized := blockRef{Number: 100, Hash: finalizedHash, Timestamp: uint64(now.Unix())}
	current := blockRef{
		Number: 101, Hash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Timestamp: uint64(now.Unix()),
	}
	return robinhoodRPCServerWithConfig(t, binding, robinhoodRPCConfig{
		Finalized: finalized, Safe: current, Latest: current,
		Blocks: map[uint64]blockRef{100: finalized, 101: current}, ReceiptBlock: finalized,
	})
}

type robinhoodRPCConfig struct {
	Finalized         blockRef
	Safe              blockRef
	Latest            blockRef
	Blocks            map[uint64]blockRef
	ReceiptBlock      blockRef
	ReceiptBlocks     map[string]blockRef
	SettlementByBlock map[uint64]uint64
	StateByBlock      map[uint64]robinhoodBlockState
}

type robinhoodBlockState struct {
	VaultAgent   string
	AgentEnabled bool
	GlobalMode   uint64
	RiskMode     uint64
}

func robinhoodTestClient(primary, secondary *httptest.Server) *RobinhoodClient {
	return &RobinhoodClient{
		primary:   &rpcEndpoint{url: primary.URL, host: "provider-one", client: primary.Client()},
		secondary: &rpcEndpoint{url: secondary.URL, host: "provider-two", client: secondary.Client()},
		finalized: make(map[string]blockRef),
	}
}

func robinhoodRPCServerWithConfig(t *testing.T, binding RobinhoodBinding, config robinhoodRPCConfig) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpc struct {
			ID     int           `json:"id"`
			Method string        `json:"method"`
			Params []interface{} `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		var result interface{}
		switch rpc.Method {
		case "eth_chainId":
			result = "0x1237"
		case "eth_syncing":
			result = false
		case "eth_getBlockByNumber":
			tag := rpc.Params[0].(string)
			var block blockRef
			switch tag {
			case "finalized":
				block = config.Finalized
			case "safe":
				block = config.Safe
			case "latest":
				block = config.Latest
			default:
				parsed, err := parseQuantity(tag)
				if err != nil {
					result = nil
					break
				}
				block = config.Blocks[parsed]
			}
			if block.Number == 0 {
				result = nil
				break
			}
			result = map[string]string{
				"number": encodeQuantity(block.Number), "hash": block.Hash,
				"timestamp": encodeQuantity(block.Timestamp),
			}
		case "eth_getProof":
			result = map[string]string{"address": binding.Vault, "codeHash": binding.VaultCodeHash}
		case "eth_getBalance":
			result = "0xde0b6b3a7640000"
		case "eth_getTransactionCount":
			result = "0x7"
		case "eth_getTransactionReceipt":
			hash := strings.ToLower(rpc.Params[0].(string))
			receiptBlock := config.ReceiptBlock
			if configured, ok := config.ReceiptBlocks[hash]; ok {
				receiptBlock = configured
			}
			to := binding.Factory
			if len(binding.ReceiptHashes) > 1 && strings.EqualFold(hash, binding.ReceiptHashes[1]) {
				to = binding.Vault
			}
			result = map[string]interface{}{
				"transactionHash": hash,
				"blockHash":       receiptBlock.Hash, "blockNumber": encodeQuantity(receiptBlock.Number),
				"status": "0x1", "to": to,
				"contractAddress": nil, "logs": []interface{}{},
			}
		case "eth_call":
			call := rpc.Params[0].(map[string]interface{})
			to := strings.ToLower(call["to"].(string))
			data := strings.ToLower(call["data"].(string))
			tag := rpc.Params[1].(string)
			number, err := parseQuantity(tag)
			if err != nil {
				result = nil
				break
			}
			block, ok := config.Blocks[number]
			if !ok {
				result = nil
				break
			}
			settlement := uint64(50_000_000)
			if configured, ok := config.SettlementByBlock[number]; ok {
				settlement = configured
			}
			state := robinhoodBlockState{
				VaultAgent: binding.Signer, AgentEnabled: true, RiskMode: 0,
			}
			if configured, ok := config.StateByBlock[number]; ok {
				state = configured
			}
			result = robinhoodCallResult(
				t,
				binding,
				to,
				data,
				time.Unix(int64(block.Timestamp), 0).UTC(),
				settlement,
				state,
			)
		default:
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
	}))
}

func robinhoodCallResult(
	t *testing.T,
	binding RobinhoodBinding,
	to string,
	data string,
	blockTime time.Time,
	settlement uint64,
	state robinhoodBlockState,
) string {
	t.Helper()
	if to == usdgAddress {
		return abiWordUint(settlement)
	}
	if to == aaplAddress {
		switch data[:10] {
		case "0x313ce567":
			return abiWordUint(18)
		case "0xa60bf13d", "0xdc767007":
			return abiWordUint(1_000_000_000_000_000_000)
		case "0x7706ba52", "0x70a08231":
			return abiWordUint(0)
		}
	}
	selector := data[:10]
	oracleFeed := "0x7777777777777777777777777777777777777777"
	sequencerFeed := "0x8888888888888888888888888888888888888888"
	if selector == "0xfeaf968c" {
		started := uint64(blockTime.Add(-10 * time.Second).Unix())
		answer := uint64(200_000_000)
		if to == sequencerFeed {
			started = uint64(blockTime.Add(-120 * time.Second).Unix())
			answer = 0
		}
		return abiWords(1, answer, started, uint64(blockTime.Add(-time.Second).Unix()), 1)
	}
	if selector == "0x8e8f294b" {
		return abiWordAddress(oracleFeed) + strings.TrimPrefix(abiWords(0, 0, 60, 1, 0, 0, 0, 0, 0), "0x")
	}
	values := map[string]string{
		"0x2724fe09": abiWordAddress(binding.Owner), "0x15600884": abiWordAddress(binding.Factory),
		"0x55bbaf1e": abiWordAddress(binding.RiskManager), "0x73068297": abiWordAddress(binding.SpotAdapter),
		"0x5c3569a2": abiWordUint(state.GlobalMode), "0x8da5cb5b": abiWordAddress(binding.Owner),
		"0xf5ff5c76": abiWordAddress(state.VaultAgent), "0x7b103999": abiWordAddress(binding.Registry),
		"0x47842663": abiWordAddress(binding.RiskManager), "0x34d45c62": abiWordAddress(binding.SpotAdapter),
		"0x99d29e71": abiWordUint(boolUint(state.AgentEnabled)), "0xae37e931": abiWordUint(1),
		"0x295a5212": abiWordUint(state.RiskMode),
		"0x3b521cb6": abiWordAddress(sequencerFeed), "0x26a97b94": abiWordUint(60),
	}
	value, ok := values[selector]
	if !ok {
		t.Fatalf("unexpected eth_call to=%s selector=%s", to, selector)
	}
	return value
}

func boolUint(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

func abiWords(values ...uint64) string {
	var result strings.Builder
	result.WriteString("0x")
	for _, value := range values {
		result.WriteString(strings.TrimPrefix(abiWordUint(value), "0x"))
	}
	return result.String()
}

func abiWordAddress(address string) string {
	return "0x" + strings.Repeat("0", 24) + strings.ToLower(address[2:])
}

func abiWordUint(value uint64) string {
	encoded := fmt.Sprintf("%x", value)
	return "0x" + strings.Repeat("0", 64-len(encoded)) + encoded
}
