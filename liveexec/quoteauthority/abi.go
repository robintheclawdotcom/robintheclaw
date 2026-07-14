package quoteauthority

import (
	"encoding/hex"
	"errors"
	"math/big"
)

const (
	selectorDecimals        = "0x313ce567"
	selectorUIMultiplier    = "0xa60bf13d"
	selectorNewUIMultiplier = "0xdc767007"
	selectorEffectiveAt     = "0x97a4064f"
	selectorOraclePaused    = "0x7706ba52"
	selectorLatestRoundData = "0xfeaf968c"
	selectorExactInputQuote = "aa9d21cb"
)

func encodeExactInputQuote(zeroForOne bool, amount *big.Int) (string, error) {
	if amount == nil || amount.Sign() <= 0 || amount.BitLen() > 128 {
		return "", errors.New("Uniswap exact input exceeds uint128")
	}
	currency0, err := addressWord(protocolSettlementToken)
	if err != nil {
		return "", err
	}
	currency1, err := addressWord(protocolStockToken)
	if err != nil {
		return "", err
	}
	hooks, err := addressWord(zeroAddress)
	if err != nil {
		return "", err
	}
	encoded := make([]byte, 0, 4+32*10)
	selector, _ := hex.DecodeString(selectorExactInputQuote)
	encoded = append(encoded, selector...)
	encoded = append(encoded, uintWord(big.NewInt(32))...)
	encoded = append(encoded, currency0...)
	encoded = append(encoded, currency1...)
	encoded = append(encoded, uintWord(new(big.Int).SetUint64(uint64(poolFee)))...)
	encoded = append(encoded, signedWord(int64(poolTickSpacing))...)
	encoded = append(encoded, hooks...)
	if zeroForOne {
		encoded = append(encoded, uintWord(big.NewInt(1))...)
	} else {
		encoded = append(encoded, make([]byte, 32)...)
	}
	encoded = append(encoded, uintWord(amount)...)
	encoded = append(encoded, uintWord(big.NewInt(256))...)
	encoded = append(encoded, make([]byte, 32)...)
	return "0x" + hex.EncodeToString(encoded), nil
}

func canonicalPoolID() (string, error) {
	currency0, err := addressWord(protocolSettlementToken)
	if err != nil {
		return "", err
	}
	currency1, err := addressWord(protocolStockToken)
	if err != nil {
		return "", err
	}
	hooks, err := addressWord(zeroAddress)
	if err != nil {
		return "", err
	}
	encoded := make([]byte, 0, 32*5)
	encoded = append(encoded, currency0...)
	encoded = append(encoded, currency1...)
	encoded = append(encoded, uintWord(new(big.Int).SetUint64(uint64(poolFee)))...)
	encoded = append(encoded, signedWord(int64(poolTickSpacing))...)
	encoded = append(encoded, hooks...)
	return runtimeCodeHash("0x" + hex.EncodeToString(encoded)), nil
}

const (
	protocolSettlementToken = "0x5fc5360d0400a0fd4f2af552add042d716f1d168"
	protocolStockToken      = "0xaf3d76f1834a1d425780943c99ea8a608f8a93f9"
)

func addressWord(value string) ([]byte, error) {
	if !validAddress(value) && value != zeroAddress {
		return nil, errors.New("invalid ABI address")
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil || len(decoded) != 20 {
		return nil, errors.New("invalid ABI address")
	}
	word := make([]byte, 32)
	copy(word[12:], decoded)
	return word, nil
}

func uintWord(value *big.Int) []byte {
	word := make([]byte, 32)
	encoded := value.Bytes()
	copy(word[len(word)-len(encoded):], encoded)
	return word
}

func signedWord(value int64) []byte {
	if value >= 0 {
		return uintWord(big.NewInt(value))
	}
	word := make([]byte, 32)
	for index := range word {
		word[index] = 0xff
	}
	encoded := big.NewInt(value).Bytes()
	copy(word[len(word)-len(encoded):], encoded)
	return word
}
