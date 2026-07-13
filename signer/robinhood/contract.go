package main

import (
	"errors"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

const vaultABIJSON = `[
  {"type":"function","name":"executeSpot","stateMutability":"nonpayable","inputs":[{"name":"intent","type":"tuple","components":[{"name":"id","type":"bytes32"},{"name":"stockToken","type":"address"},{"name":"side","type":"uint8"},{"name":"amountIn","type":"uint128"},{"name":"minAmountOut","type":"uint128"},{"name":"deadline","type":"uint64"},{"name":"configVersion","type":"uint64"}]}],"outputs":[{"name":"amountOut","type":"uint256"}]},
  {"type":"function","name":"agent","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"riskManager","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"spotAdapter","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"settlementAsset","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"admin","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"recoveryRecipient","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"guardian","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"executor","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"vault","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]}
]`

var vaultABI = mustABI(vaultABIJSON)

type abiSpotIntent struct {
	ID            [32]byte       `abi:"id"`
	StockToken    common.Address `abi:"stockToken"`
	Side          uint8          `abi:"side"`
	AmountIn      *big.Int       `abi:"amountIn"`
	MinAmountOut  *big.Int       `abi:"minAmountOut"`
	Deadline      uint64         `abi:"deadline"`
	ConfigVersion uint64         `abi:"configVersion"`
}

func packExecuteSpot(intent SpotIntent) ([]byte, error) {
	return vaultABI.Pack("executeSpot", abiSpotIntent{
		ID:            intent.ID,
		StockToken:    intent.StockToken,
		Side:          uint8(intent.Side),
		AmountIn:      intent.AmountIn,
		MinAmountOut:  intent.MinAmountOut,
		Deadline:      intent.Deadline,
		ConfigVersion: intent.ConfigVersion,
	})
}

func unpackAddress(method string, output []byte) (common.Address, error) {
	values, err := vaultABI.Unpack(method, output)
	if err != nil || len(values) != 1 {
		return common.Address{}, errors.New("invalid contract response")
	}
	address, ok := values[0].(common.Address)
	if !ok || address == (common.Address{}) {
		return common.Address{}, errors.New("invalid contract address response")
	}
	return address, nil
}

func mustABI(source string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(source))
	if err != nil {
		panic(err)
	}
	return parsed
}
