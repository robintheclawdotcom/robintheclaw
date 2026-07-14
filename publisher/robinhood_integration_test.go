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
		!observation.SignerNonceAligned || !observation.OracleHealthy || !observation.SequencerHealthy {
		t.Fatalf("unexpected observation: %+v", observation)
	}
}

func TestRobinhoodDualRPCReconcilesAtLowerCommonFinalizedHeight(t *testing.T) {
	binding := robinhoodTestBinding(t)
	commonHash := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	primary := robinhoodRPCServerAt(t, binding, 101, commonHash,
		"0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", time.Now().UTC())
	secondary := robinhoodRPCServerAt(t, binding, 100, commonHash, commonHash, time.Now().UTC())
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
	if !observation.FinalityHealthy || observation.FinalizedNumber != 100 || observation.FinalizedHash != commonHash {
		t.Fatalf("skewed healthy RPCs did not reconcile at the common height: %+v", observation)
	}
}

func TestRobinhoodDualRPCRejectsStaleCommonFinalizedHeight(t *testing.T) {
	binding := robinhoodTestBinding(t)
	hash := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	primary := robinhoodRPCServerAt(t, binding, 100, hash, hash, time.Now().UTC().Add(-31*time.Second))
	secondary := robinhoodRPCServerAt(t, binding, 100, hash, hash, time.Now().UTC().Add(-31*time.Second))
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
	if observation.FinalityHealthy || observation.WiringVerified {
		t.Fatalf("stale common head must fail closed: %+v", observation)
	}
}

func TestRobinhoodDualRPCDisagreementPublishesUnhealthy(t *testing.T) {
	binding := robinhoodTestBinding(t)
	primary := robinhoodRPCServer(t, binding, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	secondary := robinhoodRPCServer(t, binding, "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
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
	if observation.WiringVerified || observation.FinalityHealthy || observation.FundingReady {
		t.Fatalf("RPC disagreement must fail closed: %+v", observation)
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
		ReceiptHashes:       []string{"0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"},
		ExpectedSignerNonce: 7, SignerJournalReady: true,
	}
}

func robinhoodRPCServer(t *testing.T, binding RobinhoodBinding, finalizedHash string) *httptest.Server {
	return robinhoodRPCServerAt(t, binding, 100, finalizedHash, finalizedHash, time.Now().UTC())
}

func robinhoodRPCServerAt(t *testing.T, binding RobinhoodBinding, finalizedNumber uint64,
	commonHash, finalizedHash string, blockTime time.Time) *httptest.Server {
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
			number := finalizedNumber
			hash := finalizedHash
			if tag == "safe" {
				number++
				hash = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			} else if tag != "finalized" {
				parsed, err := parseQuantity(tag)
				if err != nil {
					result = nil
					break
				}
				number = parsed
				if number == 100 {
					hash = commonHash
				}
			}
			result = map[string]string{
				"number": encodeQuantity(number), "hash": hash,
				"timestamp": encodeQuantity(uint64(blockTime.Unix())),
			}
		case "eth_getProof":
			result = map[string]string{"address": binding.Vault, "codeHash": binding.VaultCodeHash}
		case "eth_getBalance":
			result = "0xde0b6b3a7640000"
		case "eth_getTransactionCount":
			result = "0x7"
		case "eth_getTransactionReceipt":
			result = map[string]interface{}{
				"transactionHash": "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
				"blockHash":       commonHash, "blockNumber": "0x64", "status": "0x1", "to": binding.Factory,
				"contractAddress": nil, "logs": []interface{}{},
			}
		case "eth_call":
			call := rpc.Params[0].(map[string]interface{})
			to := strings.ToLower(call["to"].(string))
			data := strings.ToLower(call["data"].(string))
			result = robinhoodCallResult(t, binding, to, data, blockTime)
		default:
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
	}))
}

func robinhoodCallResult(t *testing.T, binding RobinhoodBinding, to, data string, blockTime time.Time) string {
	t.Helper()
	if to == usdgAddress {
		return abiWordUint(50_000_000)
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
		return abiWordAddress(oracleFeed) + strings.TrimPrefix(abiWords(0, 0, 0, 60, 1, 0, 0, 0, 0), "0x")
	}
	values := map[string]string{
		"0x2724fe09": abiWordAddress(binding.Owner), "0x15600884": abiWordAddress(binding.Factory),
		"0x55bbaf1e": abiWordAddress(binding.RiskManager), "0x73068297": abiWordAddress(binding.SpotAdapter),
		"0x5c3569a2": abiWordUint(0), "0x8da5cb5b": abiWordAddress(binding.Owner),
		"0xf5ff5c76": abiWordAddress(binding.Signer), "0x7b103999": abiWordAddress(binding.Registry),
		"0x47842663": abiWordAddress(binding.RiskManager), "0x34d45c62": abiWordAddress(binding.SpotAdapter),
		"0x99d29e71": abiWordUint(1), "0xae37e931": abiWordUint(1), "0x295a5212": abiWordUint(0),
		"0x3b521cb6": abiWordAddress(sequencerFeed), "0x26a97b94": abiWordUint(60),
	}
	value, ok := values[selector]
	if !ok {
		t.Fatalf("unexpected eth_call to=%s selector=%s", to, selector)
	}
	return value
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
