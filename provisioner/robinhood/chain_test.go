package main

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestDeploymentActionIsExactPermissionlessFactoryCall(t *testing.T) {
	factory := common.HexToAddress("0x0000000000000000000000000000000000000010")
	owner := common.HexToAddress("0x0000000000000000000000000000000000000020")
	verifier := chainVerifier{config: config{ChainID: bigInt(4663), FactoryAddress: factory}}
	action, err := verifier.deploymentAction(owner)
	if err != nil {
		t.Fatal(err)
	}
	if action.To != "0x0000000000000000000000000000000000000010" || action.Value != "0" || action.Kind != "deploy_user_graph" {
		t.Fatalf("unexpected action: %#v", action)
	}
	data := common.FromHex(action.Data)
	if len(data) < 4 || string(data[:4]) != string(factoryABI.Methods["deploy"].ID) {
		t.Fatal("action does not call deploy")
	}
	arguments, err := factoryABI.Methods["deploy"].Inputs.Unpack(data[4:])
	if err != nil || len(arguments) != 1 || arguments[0].(common.Address) != owner {
		t.Fatalf("action owner mismatch: %#v err=%v", arguments, err)
	}
}

func TestActivationActionAuthorizesOnlyBoundSigner(t *testing.T) {
	vault := common.HexToAddress("0x0000000000000000000000000000000000000060")
	signer := common.HexToAddress("0x0000000000000000000000000000000000000030")
	client := &activationChain{vault: vault}
	verifier := chainVerifier{config: config{ChainID: bigInt(4663)}}
	record := binding{SignerAddress: strings.ToLower(signer.Hex()), Graph: graph{Vault: strings.ToLower(vault.Hex())}}

	action, err := verifier.activationActionForRPC(context.Background(), client, record)
	if err != nil {
		t.Fatal(err)
	}
	if action.Kind != "authorize_execution_agent" || action.To != strings.ToLower(vault.Hex()) || action.Value != "0" {
		t.Fatalf("unexpected action: %#v", action)
	}
	data := common.FromHex(action.Data)
	if len(data) < 4 || !bytes.Equal(data[:4], graphABI.Methods["authorizeInitialAgent"].ID) {
		t.Fatal("action does not call authorizeInitialAgent")
	}
	values, err := graphABI.Methods["authorizeInitialAgent"].Inputs.Unpack(data[4:])
	if err != nil || len(values) != 1 || values[0].(common.Address) != signer {
		t.Fatalf("authorization signer mismatch: %#v err=%v", values, err)
	}
}

type activationChain struct {
	vault   common.Address
	agent   common.Address
	enabled bool
}

func (*activationChain) ChainID(context.Context) (*big.Int, error) { return bigInt(4663), nil }
func (*activationChain) HeaderByNumber(context.Context, *big.Int) (*types.Header, error) {
	return nil, errors.New("unused")
}
func (*activationChain) CodeAt(context.Context, common.Address, *big.Int) ([]byte, error) {
	return nil, errors.New("unused")
}
func (value *activationChain) CallContract(_ context.Context, call ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	if call.To == nil || *call.To != value.vault || len(call.Data) < 4 {
		return nil, errors.New("invalid call")
	}
	switch {
	case bytes.Equal(call.Data[:4], graphABI.Methods["agent"].ID):
		return graphABI.Methods["agent"].Outputs.Pack(value.agent)
	case bytes.Equal(call.Data[:4], graphABI.Methods["agentEnabled"].ID):
		return graphABI.Methods["agentEnabled"].Outputs.Pack(value.enabled)
	default:
		return nil, errors.New("unexpected call")
	}
}
func (*activationChain) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	return nil, errors.New("unused")
}
func (*activationChain) TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error) {
	return nil, false, errors.New("unused")
}

type authorizationConfirmationChain struct {
	receipt *types.Receipt
	tx      *types.Transaction
	head    uint64
}

func (*authorizationConfirmationChain) ChainID(context.Context) (*big.Int, error) {
	return bigInt(4663), nil
}
func (value *authorizationConfirmationChain) HeaderByNumber(context.Context, *big.Int) (*types.Header, error) {
	return &types.Header{Number: new(big.Int).SetUint64(value.head)}, nil
}
func (*authorizationConfirmationChain) CodeAt(context.Context, common.Address, *big.Int) ([]byte, error) {
	return nil, errors.New("unexpected release verification")
}
func (*authorizationConfirmationChain) CallContract(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error) {
	return nil, errors.New("unexpected graph verification")
}
func (value *authorizationConfirmationChain) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	return value.receipt, nil
}
func (value *authorizationConfirmationChain) TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error) {
	return value.tx, false, nil
}

func TestAuthorizationConfirmationRejectsWrongCallAndOwner(t *testing.T) {
	ownerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	vault := common.HexToAddress("0x0000000000000000000000000000000000000060")
	signer := common.HexToAddress("0x0000000000000000000000000000000000000030")
	record := binding{
		OwnerAddress:  strings.ToLower(crypto.PubkeyToAddress(ownerKey.PublicKey).Hex()),
		SignerAddress: strings.ToLower(signer.Hex()),
		Graph:         graph{Vault: strings.ToLower(vault.Hex())},
	}
	data, err := graphABI.Pack("authorizeInitialAgent", signer)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		keyOwner bool
		to       common.Address
		data     []byte
	}{
		{name: "wrong target", keyOwner: true, to: signer, data: data},
		{name: "wrong calldata", keyOwner: true, to: vault, data: append([]byte(nil), data[:len(data)-1]...)},
		{name: "wrong sender", keyOwner: false, to: vault, data: data},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := otherKey
			if test.keyOwner {
				key = ownerKey
			}
			tx, err := types.SignNewTx(key, types.LatestSignerForChainID(bigInt(4663)), &types.LegacyTx{
				Nonce: 1, To: &test.to, Value: big.NewInt(0), Gas: 100_000, GasPrice: big.NewInt(1), Data: test.data,
			})
			if err != nil {
				t.Fatal(err)
			}
			client := &authorizationConfirmationChain{
				tx: tx, head: 20,
				receipt: &types.Receipt{TxHash: tx.Hash(), Status: types.ReceiptStatusSuccessful, BlockNumber: big.NewInt(10), BlockHash: common.HexToHash("0x01")},
			}
			verifier := chainVerifier{config: config{ChainID: bigInt(4663), FinalityBlocks: 2}, primary: client, secondary: client}
			if _, err := verifier.confirmAuthorization(context.Background(), record, tx.Hash()); err == nil {
				t.Fatal("invalid authorization transaction was accepted")
			}
		})
	}
}

func TestAuthorizationConfirmationRejectsRevertedAndNonFinalReceipts(t *testing.T) {
	ownerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	vault := common.HexToAddress("0x0000000000000000000000000000000000000060")
	signer := common.HexToAddress("0x0000000000000000000000000000000000000030")
	data, err := graphABI.Pack("authorizeInitialAgent", signer)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := types.SignNewTx(ownerKey, types.LatestSignerForChainID(bigInt(4663)), &types.LegacyTx{
		Nonce: 1, To: &vault, Value: big.NewInt(0), Gas: 100_000, GasPrice: big.NewInt(1), Data: data,
	})
	if err != nil {
		t.Fatal(err)
	}
	record := binding{
		OwnerAddress:  strings.ToLower(crypto.PubkeyToAddress(ownerKey.PublicKey).Hex()),
		SignerAddress: strings.ToLower(signer.Hex()),
		Graph:         graph{Vault: strings.ToLower(vault.Hex())},
	}
	for _, test := range []struct {
		name   string
		status uint64
		head   uint64
	}{
		{name: "reverted", status: types.ReceiptStatusFailed, head: 20},
		{name: "non final", status: types.ReceiptStatusSuccessful, head: 11},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &authorizationConfirmationChain{
				tx: tx, head: test.head,
				receipt: &types.Receipt{TxHash: tx.Hash(), Status: test.status, BlockNumber: big.NewInt(10), BlockHash: common.HexToHash("0x01")},
			}
			verifier := chainVerifier{config: config{ChainID: bigInt(4663), FinalityBlocks: 2}, primary: client, secondary: client}
			if _, err := verifier.confirmAuthorization(context.Background(), record, tx.Hash()); err == nil {
				t.Fatal("invalid authorization receipt was accepted")
			}
		})
	}
}
