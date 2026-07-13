package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		!observation.OwnerGasReady || !observation.SignerGasReady || !observation.AgentEnabled || !observation.Flat {
		t.Fatalf("unexpected observation: %+v", observation)
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
	dir := t.TempDir()
	journal := filepath.Join(dir, "receipts")
	encoded := `{"vault":"0x9999999999999999999999999999999999999999","hashes":["0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"]}`
	if err := os.WriteFile(journal, []byte(encoded), 0o600); err != nil {
		t.Fatal(err)
	}
	return RobinhoodBinding{
		Registry: "0x6666666666666666666666666666666666666666", Factory: "0x2222222222222222222222222222222222222222",
		Vault: "0x9999999999999999999999999999999999999999", RiskManager: "0x3333333333333333333333333333333333333333",
		SpotAdapter: "0x4444444444444444444444444444444444444444", Owner: "0x1111111111111111111111111111111111111111",
		Signer:               "0x5555555555555555555555555555555555555555",
		VaultCodeHash:        "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		MinimumSettlementRaw: "25000000", MinimumOwnerGasRaw: "1", MinimumSignerGasRaw: "1", ReceiptJournalFile: journal,
	}
}

func robinhoodRPCServer(t *testing.T, binding RobinhoodBinding, finalizedHash string) *httptest.Server {
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
			switch tag {
			case "finalized", "0x64":
				result = map[string]string{"number": "0x64", "hash": finalizedHash}
			case "safe":
				result = map[string]string{"number": "0x65", "hash": "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
			default:
				result = nil
			}
		case "eth_getProof":
			result = map[string]string{"address": binding.Vault, "codeHash": binding.VaultCodeHash}
		case "eth_getBalance":
			result = "0xde0b6b3a7640000"
		case "eth_getTransactionReceipt":
			result = map[string]interface{}{
				"transactionHash": "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
				"blockHash":       finalizedHash, "blockNumber": "0x64", "status": "0x1", "to": binding.Factory,
				"contractAddress": nil, "logs": []interface{}{},
			}
		case "eth_call":
			call := rpc.Params[0].(map[string]interface{})
			to := strings.ToLower(call["to"].(string))
			data := strings.ToLower(call["data"].(string))
			result = robinhoodCallResult(t, binding, to, data)
		default:
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
	}))
}

func robinhoodCallResult(t *testing.T, binding RobinhoodBinding, to, data string) string {
	t.Helper()
	if to == usdgAddress {
		return abiWordUint(50_000_000)
	}
	if to == aaplAddress {
		return abiWordUint(0)
	}
	selector := data[:10]
	values := map[string]string{
		"0x2724fe09": abiWordAddress(binding.Owner), "0x15600884": abiWordAddress(binding.Factory),
		"0x55bbaf1e": abiWordAddress(binding.RiskManager), "0x73068297": abiWordAddress(binding.SpotAdapter),
		"0x5c3569a2": abiWordUint(0), "0x8da5cb5b": abiWordAddress(binding.Owner),
		"0xf5ff5c76": abiWordAddress(binding.Signer), "0x7b103999": abiWordAddress(binding.Registry),
		"0x47842663": abiWordAddress(binding.RiskManager), "0x34d45c62": abiWordAddress(binding.SpotAdapter),
		"0x99d29e71": abiWordUint(1), "0xae37e931": abiWordUint(1), "0x295a5212": abiWordUint(0),
	}
	value, ok := values[selector]
	if !ok {
		t.Fatalf("unexpected eth_call to=%s selector=%s", to, selector)
	}
	return value
}

func abiWordAddress(address string) string {
	return "0x" + strings.Repeat("0", 24) + strings.ToLower(address[2:])
}

func abiWordUint(value uint64) string {
	encoded := fmt.Sprintf("%x", value)
	return "0x" + strings.Repeat("0", 64-len(encoded)) + encoded
}
