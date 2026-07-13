package main

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
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
