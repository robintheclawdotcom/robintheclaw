package sequencerpublisher

import (
	"bytes"
	"context"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	mainnetUSDG = common.HexToAddress("0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168")
	mainnetAAPL = common.HexToAddress("0xaF3D76f1834A1d425780943C99Ea8A608f8a93f9")

	implementationSlot           = common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")
	beaconSlot                   = common.HexToHash("0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50")
	beaconImplementationSelector = []byte{0x5c, 0x60, 0xda, 0x1b}
)

type DependencyPins struct {
	USDGProxyCodeHash          common.Hash
	USDGImplementation         common.Address
	USDGImplementationCodeHash common.Hash
	AAPLProxyCodeHash          common.Hash
	AAPLBeacon                 common.Address
	AAPLBeaconCodeHash         common.Hash
	AAPLImplementation         common.Address
	AAPLImplementationCodeHash common.Hash
}

func (pins DependencyPins) Validate() error {
	if pins.USDGProxyCodeHash == (common.Hash{}) ||
		pins.USDGImplementation == (common.Address{}) ||
		pins.USDGImplementationCodeHash == (common.Hash{}) {
		return errors.New("USDG dependency identity is incomplete")
	}
	if pins.AAPLProxyCodeHash == (common.Hash{}) ||
		pins.AAPLBeacon == (common.Address{}) ||
		pins.AAPLBeaconCodeHash == (common.Hash{}) ||
		pins.AAPLImplementation == (common.Address{}) ||
		pins.AAPLImplementationCodeHash == (common.Hash{}) {
		return errors.New("AAPL dependency identity is incomplete")
	}
	if pins.USDGImplementation == mainnetUSDG ||
		pins.AAPLBeacon == mainnetAAPL ||
		pins.AAPLImplementation == mainnetAAPL ||
		pins.AAPLImplementation == pins.AAPLBeacon {
		return errors.New("dependency identity aliases a proxy")
	}
	return nil
}

func verifyDependencies(
	ctx context.Context,
	source Chain,
	transaction Chain,
	block *big.Int,
	pins DependencyPins,
) string {
	if err := pins.Validate(); err != nil {
		return "dependency_config"
	}
	if reason := verifyCode(
		ctx, source, transaction, mainnetUSDG, block, pins.USDGProxyCodeHash,
		"usdg_proxy_unavailable", "usdg_proxy_code_mismatch",
	); reason != "" {
		return reason
	}
	implementation, reason := readStorageAddress(
		ctx, source, transaction, mainnetUSDG, implementationSlot, block, "usdg_implementation_unavailable",
	)
	if reason != "" {
		return reason
	}
	if implementation != pins.USDGImplementation {
		return "usdg_implementation_mismatch"
	}
	if reason := verifyCode(
		ctx, source, transaction, implementation, block, pins.USDGImplementationCodeHash,
		"usdg_implementation_unavailable", "usdg_implementation_code_mismatch",
	); reason != "" {
		return reason
	}

	if reason := verifyCode(
		ctx, source, transaction, mainnetAAPL, block, pins.AAPLProxyCodeHash,
		"aapl_proxy_unavailable", "aapl_proxy_code_mismatch",
	); reason != "" {
		return reason
	}
	beacon, reason := readStorageAddress(
		ctx, source, transaction, mainnetAAPL, beaconSlot, block, "aapl_beacon_unavailable",
	)
	if reason != "" {
		return reason
	}
	if beacon != pins.AAPLBeacon {
		return "aapl_beacon_mismatch"
	}
	if reason := verifyCode(
		ctx, source, transaction, beacon, block, pins.AAPLBeaconCodeHash,
		"aapl_beacon_unavailable", "aapl_beacon_code_mismatch",
	); reason != "" {
		return reason
	}
	implementation, reason = readBeaconImplementation(ctx, source, transaction, beacon, block)
	if reason != "" {
		return reason
	}
	if implementation != pins.AAPLImplementation {
		return "aapl_implementation_mismatch"
	}
	return verifyCode(
		ctx, source, transaction, implementation, block, pins.AAPLImplementationCodeHash,
		"aapl_implementation_unavailable", "aapl_implementation_code_mismatch",
	)
}

func verifyCode(
	ctx context.Context,
	source Chain,
	transaction Chain,
	address common.Address,
	block *big.Int,
	expected common.Hash,
	unavailableReason string,
	mismatchReason string,
) string {
	sourceCode, err := source.CodeAt(ctx, address, block)
	if err != nil || len(sourceCode) == 0 {
		return unavailableReason
	}
	transactionCode, err := transaction.CodeAt(ctx, address, block)
	if err != nil || len(transactionCode) == 0 {
		return unavailableReason
	}
	if !bytes.Equal(sourceCode, transactionCode) {
		return "dependency_rpc_disagreement"
	}
	if crypto.Keccak256Hash(sourceCode) != expected {
		return mismatchReason
	}
	return ""
}

func readStorageAddress(
	ctx context.Context,
	source Chain,
	transaction Chain,
	address common.Address,
	slot common.Hash,
	block *big.Int,
	unavailableReason string,
) (common.Address, string) {
	sourceValue, err := source.StorageAt(ctx, address, slot, block)
	if err != nil {
		return common.Address{}, unavailableReason
	}
	transactionValue, err := transaction.StorageAt(ctx, address, slot, block)
	if err != nil {
		return common.Address{}, unavailableReason
	}
	if !bytes.Equal(sourceValue, transactionValue) {
		return common.Address{}, "dependency_rpc_disagreement"
	}
	value, ok := decodeAddressWord(sourceValue)
	if !ok {
		return common.Address{}, unavailableReason
	}
	return value, ""
}

func readBeaconImplementation(
	ctx context.Context,
	source Chain,
	transaction Chain,
	beacon common.Address,
	block *big.Int,
) (common.Address, string) {
	message := ethereum.CallMsg{To: &beacon, Data: beaconImplementationSelector}
	sourceValue, err := source.CallContract(ctx, message, block)
	if err != nil {
		return common.Address{}, "aapl_implementation_unavailable"
	}
	transactionValue, err := transaction.CallContract(ctx, message, block)
	if err != nil {
		return common.Address{}, "aapl_implementation_unavailable"
	}
	if !bytes.Equal(sourceValue, transactionValue) {
		return common.Address{}, "dependency_rpc_disagreement"
	}
	value, ok := decodeAddressWord(sourceValue)
	if !ok {
		return common.Address{}, "aapl_implementation_unavailable"
	}
	return value, ""
}

func decodeAddressWord(value []byte) (common.Address, bool) {
	if len(value) != common.HashLength || !bytes.Equal(value[:12], make([]byte, 12)) {
		return common.Address{}, false
	}
	address := common.BytesToAddress(value[12:])
	return address, address != (common.Address{})
}
