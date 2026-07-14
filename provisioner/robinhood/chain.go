package main

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const factoryABIJSON = `[
  {"type":"function","name":"deploy","stateMutability":"nonpayable","inputs":[{"name":"owner","type":"address"}],"outputs":[{"name":"graph","type":"tuple","components":[{"name":"riskManager","type":"address"},{"name":"spotAdapter","type":"address"},{"name":"vault","type":"address"}]}]},
  {"type":"function","name":"predictGraph","stateMutability":"view","inputs":[{"name":"owner","type":"address"}],"outputs":[{"name":"graph","type":"tuple","components":[{"name":"riskManager","type":"address"},{"name":"spotAdapter","type":"address"},{"name":"vault","type":"address"}]}]},
  {"type":"function","name":"graphForOwner","stateMutability":"view","inputs":[{"name":"owner","type":"address"}],"outputs":[{"name":"graph","type":"tuple","components":[{"name":"riskManager","type":"address"},{"name":"spotAdapter","type":"address"},{"name":"vault","type":"address"}]}]},
  {"type":"function","name":"registry","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"policyDigest","stateMutability":"view","inputs":[],"outputs":[{"type":"bytes32"}]}
]`

const registryABIJSON = `[
  {"type":"function","name":"isFactoryApproved","stateMutability":"view","inputs":[{"type":"address"}],"outputs":[{"type":"bool"}]},
  {"type":"function","name":"ownerOfVault","stateMutability":"view","inputs":[{"type":"address"}],"outputs":[{"type":"address"}]},
  {"type":"function","name":"factoryOfVault","stateMutability":"view","inputs":[{"type":"address"}],"outputs":[{"type":"address"}]},
  {"type":"function","name":"riskManagerOfVault","stateMutability":"view","inputs":[{"type":"address"}],"outputs":[{"type":"address"}]},
  {"type":"function","name":"spotAdapterOfVault","stateMutability":"view","inputs":[{"type":"address"}],"outputs":[{"type":"address"}]}
]`

const graphABIJSON = `[
  {"type":"function","name":"authorizeInitialAgent","stateMutability":"nonpayable","inputs":[{"type":"address"}],"outputs":[]},
  {"type":"function","name":"owner","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"agent","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"agentEnabled","stateMutability":"view","inputs":[],"outputs":[{"type":"bool"}]},
  {"type":"function","name":"riskManager","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"spotAdapter","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"registry","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"executor","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"treasury","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"configAdmin","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"vault","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]}
]`

var (
	factoryABI  = mustABI(factoryABIJSON)
	registryABI = mustABI(registryABIJSON)
	graphABI    = mustABI(graphABIJSON)
)

type chainClient interface {
	ChainID(context.Context) (*big.Int, error)
	HeaderByNumber(context.Context, *big.Int) (*types.Header, error)
	CodeAt(context.Context, common.Address, *big.Int) ([]byte, error)
	CallContract(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error)
	TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error)
	TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error)
}

type chainVerifier struct {
	config    config
	primary   chainClient
	secondary chainClient
}

type abiGraph struct {
	RiskManager common.Address `abi:"riskManager"`
	SpotAdapter common.Address `abi:"spotAdapter"`
	Vault       common.Address `abi:"vault"`
}

func (value *chainVerifier) predict(ctx context.Context, owner common.Address) (graph, error) {
	var expected graph
	for index, client := range []chainClient{value.primary, value.secondary} {
		if err := value.verifyRelease(ctx, client, nil); err != nil {
			return graph{}, fmt.Errorf("release verification %d: %w", index+1, err)
		}
		predicted, err := value.readGraph(ctx, client, "predictGraph", owner, nil)
		if err != nil {
			return graph{}, err
		}
		if index == 0 {
			expected = predicted
		} else if predicted != expected {
			return graph{}, errors.New("RPCs disagree on deterministic graph")
		}
	}
	if expected.Vault == strings.ToLower((common.Address{}).Hex()) || expected.RiskManager == expected.SpotAdapter || expected.RiskManager == expected.Vault || expected.SpotAdapter == expected.Vault {
		return graph{}, errors.New("factory returned invalid deterministic graph")
	}
	return expected, nil
}

func (value *chainVerifier) deploymentAction(owner common.Address) (unsignedAction, error) {
	data, err := factoryABI.Pack("deploy", owner)
	if err != nil {
		return unsignedAction{}, errors.New("encode factory deployment")
	}
	return unsignedAction{
		Kind:    "deploy_user_graph",
		ChainID: value.config.ChainID.String(),
		To:      strings.ToLower(value.config.FactoryAddress.Hex()),
		Value:   "0",
		Data:    "0x" + strings.ToLower(common.Bytes2Hex(data)),
	}, nil
}

func (value *chainVerifier) activationAction(ctx context.Context, record binding) (*unsignedAction, error) {
	var expected *unsignedAction
	for index, client := range []chainClient{value.primary, value.secondary} {
		if err := value.verifyRelease(ctx, client, nil); err != nil {
			return nil, fmt.Errorf("release verification %d: %w", index+1, err)
		}
		if err := value.verifyGraph(ctx, client, record, nil); err != nil {
			return nil, fmt.Errorf("graph verification %d: %w", index+1, err)
		}
		action, err := value.activationActionForRPC(ctx, client, record)
		if err != nil {
			return nil, fmt.Errorf("agent verification %d: %w", index+1, err)
		}
		if index == 0 {
			expected = action
		} else if !sameAction(expected, action) {
			return nil, errors.New("RPCs disagree on agent authorization")
		}
	}
	return expected, nil
}

func (value *chainVerifier) activationActionForRPC(ctx context.Context, client chainClient, record binding) (*unsignedAction, error) {
	return value.activationActionForRPCAtBlock(ctx, client, record, nil)
}

func (value *chainVerifier) activationActionForRPCAtBlock(ctx context.Context, client chainClient, record binding, block *big.Int) (*unsignedAction, error) {
	vault := common.HexToAddress(record.Graph.Vault)
	signer := common.HexToAddress(record.SignerAddress)
	agent, err := value.readAddressAllowZero(ctx, client, graphABI, vault, "agent", block)
	if err != nil {
		return nil, errors.New("read vault agent")
	}
	enabled, err := value.readBool(ctx, client, graphABI, vault, "agentEnabled", block)
	if err != nil {
		return nil, errors.New("read vault agent authorization")
	}
	if agent != (common.Address{}) && agent != signer {
		return nil, errors.New("vault agent does not match execution signer")
	}
	if enabled {
		if agent != signer {
			return nil, errors.New("vault enabled an invalid execution agent")
		}
		return nil, nil
	}
	data, err := graphABI.Pack("authorizeInitialAgent", signer)
	if err != nil {
		return nil, errors.New("encode owner agent authorization")
	}
	return &unsignedAction{
		Kind:    "authorize_execution_agent",
		ChainID: value.config.ChainID.String(),
		To:      strings.ToLower(vault.Hex()),
		Value:   "0",
		Data:    "0x" + strings.ToLower(common.Bytes2Hex(data)),
	}, nil
}

func (value *chainVerifier) verifyActive(ctx context.Context, record binding) error {
	action, err := value.activationAction(ctx, record)
	if err != nil {
		return err
	}
	if action != nil {
		return errors.New("owner has not authorized the execution agent")
	}
	return nil
}

func (value *chainVerifier) confirmDeployment(ctx context.Context, record binding, txHash common.Hash) (uint64, error) {
	owner := common.HexToAddress(record.OwnerAddress)
	expectedData, err := factoryABI.Pack("deploy", owner)
	if err != nil {
		return 0, errors.New("encode expected deployment")
	}
	var confirmedBlock uint64
	var confirmedHash common.Hash
	for index, client := range []chainClient{value.primary, value.secondary} {
		receipt, err := client.TransactionReceipt(ctx, txHash)
		if err != nil || receipt == nil || receipt.TxHash != txHash || receipt.Status != types.ReceiptStatusSuccessful || receipt.BlockNumber == nil {
			return 0, errors.New("deployment receipt is not successful on both RPCs")
		}
		transaction, pending, err := client.TransactionByHash(ctx, txHash)
		if err != nil || pending || transaction == nil || transaction.To() == nil ||
			*transaction.To() != value.config.FactoryAddress || transaction.Value().Sign() != 0 ||
			transaction.Hash() != txHash || transaction.ChainId().Cmp(value.config.ChainID) != 0 || !equalBytes(transaction.Data(), expectedData) {
			return 0, errors.New("deployment transaction does not match prepared factory call")
		}
		head, err := client.HeaderByNumber(ctx, nil)
		if err != nil || head.Number == nil || head.Number.Uint64() < receipt.BlockNumber.Uint64()+value.config.FinalityBlocks {
			return 0, errors.New("deployment transaction is not final")
		}
		if index == 0 {
			confirmedBlock = receipt.BlockNumber.Uint64()
			confirmedHash = receipt.BlockHash
		} else if receipt.BlockNumber.Uint64() != confirmedBlock || receipt.BlockHash != confirmedHash {
			return 0, errors.New("RPCs disagree on deployment receipt")
		}
		block := receipt.BlockNumber
		if err := value.verifyRelease(ctx, client, block); err != nil {
			return 0, err
		}
		if err := value.verifyGraph(ctx, client, record, block); err != nil {
			return 0, err
		}
	}
	return confirmedBlock, nil
}

func (value *chainVerifier) confirmAuthorization(ctx context.Context, record binding, txHash common.Hash) (uint64, error) {
	expectedData, err := graphABI.Pack("authorizeInitialAgent", common.HexToAddress(record.SignerAddress))
	if err != nil {
		return 0, errors.New("encode expected agent authorization")
	}
	var confirmedBlock uint64
	var confirmedHash common.Hash
	for index, client := range []chainClient{value.primary, value.secondary} {
		receipt, err := client.TransactionReceipt(ctx, txHash)
		if err != nil || receipt == nil || receipt.TxHash != txHash || receipt.Status != types.ReceiptStatusSuccessful || receipt.BlockNumber == nil {
			return 0, errors.New("authorization receipt is not successful on both RPCs")
		}
		transaction, pending, err := client.TransactionByHash(ctx, txHash)
		if err != nil || pending || transaction == nil || transaction.Hash() != txHash || transaction.To() == nil ||
			*transaction.To() != common.HexToAddress(record.Graph.Vault) || transaction.Value().Sign() != 0 ||
			transaction.ChainId().Cmp(value.config.ChainID) != 0 || !equalBytes(transaction.Data(), expectedData) {
			return 0, errors.New("authorization transaction does not match prepared vault call")
		}
		sender, err := types.Sender(types.LatestSignerForChainID(value.config.ChainID), transaction)
		if err != nil || sender != common.HexToAddress(record.OwnerAddress) {
			return 0, errors.New("authorization transaction was not signed by the vault owner")
		}
		head, err := client.HeaderByNumber(ctx, nil)
		if err != nil || head.Number == nil || head.Number.Uint64() < receipt.BlockNumber.Uint64()+value.config.FinalityBlocks {
			return 0, errors.New("authorization transaction is not final")
		}
		if index == 0 {
			confirmedBlock = receipt.BlockNumber.Uint64()
			confirmedHash = receipt.BlockHash
		} else if receipt.BlockNumber.Uint64() != confirmedBlock || receipt.BlockHash != confirmedHash {
			return 0, errors.New("RPCs disagree on authorization receipt")
		}
		block := receipt.BlockNumber
		if err := value.verifyRelease(ctx, client, block); err != nil {
			return 0, err
		}
		if err := value.verifyGraph(ctx, client, record, block); err != nil {
			return 0, err
		}
		action, err := value.activationActionForRPCAtBlock(ctx, client, record, block)
		if err != nil || action != nil {
			return 0, errors.New("authorization state does not match the bound execution signer")
		}
	}
	return confirmedBlock, nil
}

func (value *chainVerifier) verifyRelease(ctx context.Context, client chainClient, block *big.Int) error {
	chainID, err := client.ChainID(ctx)
	if err != nil || chainID.Cmp(value.config.ChainID) != 0 {
		return errors.New("Robinhood chain ID mismatch")
	}
	for _, contract := range []struct {
		address common.Address
		hash    common.Hash
		name    string
	}{
		{value.config.FactoryAddress, value.config.FactoryCodeHash, "factory"},
		{value.config.RegistryAddress, value.config.RegistryCodeHash, "registry"},
	} {
		code, err := client.CodeAt(ctx, contract.address, block)
		if err != nil || len(code) == 0 || crypto.Keccak256Hash(code) != contract.hash {
			return fmt.Errorf("%s runtime code mismatch", contract.name)
		}
	}
	registry, err := value.readAddress(ctx, client, factoryABI, value.config.FactoryAddress, "registry", block)
	if err != nil || registry != value.config.RegistryAddress {
		return errors.New("factory registry mismatch")
	}
	digest, err := value.readHash(ctx, client, factoryABI, value.config.FactoryAddress, "policyDigest", block)
	if err != nil || digest != value.config.PolicyDigest {
		return errors.New("factory policy digest mismatch")
	}
	approved, err := value.readBool(ctx, client, registryABI, value.config.RegistryAddress, "isFactoryApproved", block, value.config.FactoryAddress)
	if err != nil || !approved {
		return errors.New("factory is not approved by registry")
	}
	return nil
}

func (value *chainVerifier) verifyGraph(ctx context.Context, client chainClient, record binding, block *big.Int) error {
	owner := common.HexToAddress(record.OwnerAddress)
	vault := common.HexToAddress(record.Graph.Vault)
	risk := common.HexToAddress(record.Graph.RiskManager)
	adapter := common.HexToAddress(record.Graph.SpotAdapter)
	deployed, err := value.readGraph(ctx, client, "graphForOwner", owner, block)
	if err != nil || deployed != record.Graph {
		return errors.New("factory graph binding mismatch")
	}
	for _, contract := range []struct {
		address common.Address
		hash    common.Hash
		name    string
	}{
		{vault, value.config.VaultCodeHash, "vault"},
		{risk, value.config.RiskManagerCodeHash, "risk manager"},
		{adapter, value.config.SpotAdapterCodeHash, "spot adapter"},
	} {
		code, err := client.CodeAt(ctx, contract.address, block)
		if err != nil || len(code) == 0 || crypto.Keccak256Hash(code) != contract.hash {
			return fmt.Errorf("%s runtime code mismatch", contract.name)
		}
	}
	checks := []struct {
		contract    common.Address
		contractABI abi.ABI
		method      string
		expected    common.Address
	}{
		{value.config.RegistryAddress, registryABI, "ownerOfVault", owner},
		{value.config.RegistryAddress, registryABI, "factoryOfVault", value.config.FactoryAddress},
		{value.config.RegistryAddress, registryABI, "riskManagerOfVault", risk},
		{value.config.RegistryAddress, registryABI, "spotAdapterOfVault", adapter},
		{vault, graphABI, "owner", owner},
		{vault, graphABI, "riskManager", risk},
		{vault, graphABI, "spotAdapter", adapter},
		{vault, graphABI, "registry", value.config.RegistryAddress},
		{risk, graphABI, "executor", vault},
		{risk, graphABI, "treasury", owner},
		{risk, graphABI, "configAdmin", value.config.RegistryAddress},
		{adapter, graphABI, "vault", vault},
		{adapter, graphABI, "configAdmin", value.config.RegistryAddress},
	}
	for _, check := range checks {
		arguments := []any{}
		if check.contract == value.config.RegistryAddress {
			arguments = append(arguments, vault)
		}
		actual, err := value.readAddress(ctx, client, check.contractABI, check.contract, check.method, block, arguments...)
		if err != nil || actual != check.expected {
			return fmt.Errorf("%s binding mismatch", check.method)
		}
	}
	return nil
}

func (value *chainVerifier) readAddressAllowZero(ctx context.Context, client chainClient, contractABI abi.ABI, contract common.Address, method string, block *big.Int, args ...any) (common.Address, error) {
	output, err := call(ctx, client, contractABI, contract, method, block, args...)
	if err != nil {
		return common.Address{}, err
	}
	values, err := contractABI.Unpack(method, output)
	if err != nil || len(values) != 1 {
		return common.Address{}, errors.New("invalid address response")
	}
	address, ok := values[0].(common.Address)
	if !ok {
		return common.Address{}, errors.New("invalid address response")
	}
	return address, nil
}

func sameAction(left, right *unsignedAction) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (value *chainVerifier) readGraph(ctx context.Context, client chainClient, method string, owner common.Address, block *big.Int) (graph, error) {
	output, err := call(ctx, client, factoryABI, value.config.FactoryAddress, method, block, owner)
	if err != nil {
		return graph{}, err
	}
	values, err := factoryABI.Unpack(method, output)
	if err != nil || len(values) != 1 {
		return graph{}, errors.New("invalid factory graph response")
	}
	converted, ok := abi.ConvertType(values[0], new(abiGraph)).(*abiGraph)
	if !ok || converted.RiskManager == (common.Address{}) || converted.SpotAdapter == (common.Address{}) || converted.Vault == (common.Address{}) {
		return graph{}, errors.New("invalid factory graph response")
	}
	return graph{
		RiskManager: strings.ToLower(converted.RiskManager.Hex()),
		SpotAdapter: strings.ToLower(converted.SpotAdapter.Hex()),
		Vault:       strings.ToLower(converted.Vault.Hex()),
	}, nil
}

func (value *chainVerifier) readAddress(ctx context.Context, client chainClient, contractABI abi.ABI, contract common.Address, method string, block *big.Int, args ...any) (common.Address, error) {
	output, err := call(ctx, client, contractABI, contract, method, block, args...)
	if err != nil {
		return common.Address{}, err
	}
	values, err := contractABI.Unpack(method, output)
	if err != nil || len(values) != 1 {
		return common.Address{}, errors.New("invalid address response")
	}
	address, ok := values[0].(common.Address)
	if !ok || address == (common.Address{}) {
		return common.Address{}, errors.New("invalid address response")
	}
	return address, nil
}

func (value *chainVerifier) readHash(ctx context.Context, client chainClient, contractABI abi.ABI, contract common.Address, method string, block *big.Int) (common.Hash, error) {
	output, err := call(ctx, client, contractABI, contract, method, block)
	if err != nil {
		return common.Hash{}, err
	}
	values, err := contractABI.Unpack(method, output)
	if err != nil || len(values) != 1 {
		return common.Hash{}, errors.New("invalid hash response")
	}
	hash, ok := values[0].([32]byte)
	if !ok || common.BytesToHash(hash[:]) == (common.Hash{}) {
		return common.Hash{}, errors.New("invalid hash response")
	}
	return common.BytesToHash(hash[:]), nil
}

func (value *chainVerifier) readBool(ctx context.Context, client chainClient, contractABI abi.ABI, contract common.Address, method string, block *big.Int, args ...any) (bool, error) {
	output, err := call(ctx, client, contractABI, contract, method, block, args...)
	if err != nil {
		return false, err
	}
	values, err := contractABI.Unpack(method, output)
	if err != nil || len(values) != 1 {
		return false, errors.New("invalid boolean response")
	}
	result, ok := values[0].(bool)
	if !ok {
		return false, errors.New("invalid boolean response")
	}
	return result, nil
}

func call(ctx context.Context, client chainClient, contractABI abi.ABI, contract common.Address, method string, block *big.Int, args ...any) ([]byte, error) {
	data, err := contractABI.Pack(method, args...)
	if err != nil {
		return nil, errors.New("encode contract call")
	}
	output, err := client.CallContract(ctx, ethereum.CallMsg{To: &contract, Data: data}, block)
	if err != nil {
		return nil, errors.New("contract call failed")
	}
	return output, nil
}

func mustABI(source string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(source))
	if err != nil {
		panic(err)
	}
	return parsed
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
