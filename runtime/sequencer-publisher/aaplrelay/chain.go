package aaplrelay

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const sourceABIJSON = `[
  {"type":"function","name":"decimals","stateMutability":"view","inputs":[],"outputs":[{"type":"uint8"}]},
  {"type":"function","name":"description","stateMutability":"view","inputs":[],"outputs":[{"type":"string"}]},
  {"type":"function","name":"latestRoundData","stateMutability":"view","inputs":[],"outputs":[{"type":"uint80"},{"type":"int256"},{"type":"uint256"},{"type":"uint256"},{"type":"uint80"}]}
]`

const targetABIJSON = `[
  {"type":"function","name":"report","stateMutability":"nonpayable","inputs":[{"type":"uint64"},{"type":"uint80"},{"type":"int192"},{"type":"uint64"},{"type":"uint80"}],"outputs":[]},
  {"type":"function","name":"reports","stateMutability":"view","inputs":[{"type":"address"}],"outputs":[{"type":"uint64"},{"type":"uint64"},{"type":"uint64"},{"type":"uint80"},{"type":"uint80"},{"type":"int192"}]},
  {"type":"function","name":"publisher1","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"publisher2","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"publisher3","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
  {"type":"function","name":"publisherCount","stateMutability":"view","inputs":[],"outputs":[{"type":"uint8"}]},
  {"type":"function","name":"quorum","stateMutability":"view","inputs":[],"outputs":[{"type":"uint8"}]},
  {"type":"function","name":"maxReportAge","stateMutability":"view","inputs":[],"outputs":[{"type":"uint64"}]},
  {"type":"function","name":"maxSourceAge","stateMutability":"view","inputs":[],"outputs":[{"type":"uint64"}]},
  {"type":"function","name":"decimals","stateMutability":"view","inputs":[],"outputs":[{"type":"uint8"}]}
]`

type Chain interface {
	ChainID(context.Context) (*big.Int, error)
	HeaderByNumber(context.Context, *big.Int) (*types.Header, error)
	CodeAt(context.Context, common.Address, *big.Int) ([]byte, error)
	CallContract(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error)
	EstimateGas(context.Context, ethereum.CallMsg) (uint64, error)
	SuggestGasTipCap(context.Context) (*big.Int, error)
	PendingNonceAt(context.Context, common.Address) (uint64, error)
	BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error)
	SendTransaction(context.Context, *types.Transaction) error
	TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error)
	TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error)
}

type PriceObservation struct {
	RoundID         *big.Int
	Answer          *big.Int
	StartedAt       uint64
	UpdatedAt       uint64
	AnsweredInRound *big.Int
	BlockNumber     uint64
	BlockHash       common.Hash
	ObservedAt      time.Time
}

func (observation PriceObservation) Equal(other PriceObservation) bool {
	return observation.RoundID.Cmp(other.RoundID) == 0 && observation.Answer.Cmp(other.Answer) == 0 &&
		observation.StartedAt == other.StartedAt && observation.UpdatedAt == other.UpdatedAt &&
		observation.AnsweredInRound.Cmp(other.AnsweredInRound) == 0 &&
		observation.BlockNumber == other.BlockNumber && observation.BlockHash == other.BlockHash
}

type SourceReader struct {
	first       Chain
	second      Chain
	feed        common.Address
	codeHash    common.Hash
	contract    abi.ABI
	maxBlockAge time.Duration
	clock       func() time.Time
}

func NewSourceReader(first, second Chain, config Config) (*SourceReader, error) {
	contract, err := abi.JSON(strings.NewReader(sourceABIJSON))
	if err != nil {
		return nil, errors.New("parse AAPL source ABI")
	}
	return &SourceReader{
		first: first, second: second, feed: config.SourceFeed, codeHash: config.SourceCodeHash,
		contract: contract, maxBlockAge: config.FinalizedMaxAge, clock: time.Now,
	}, nil
}

func (reader *SourceReader) Observe(ctx context.Context) (PriceObservation, error) {
	for _, chain := range []Chain{reader.first, reader.second} {
		id, err := chain.ChainID(ctx)
		if err != nil || id.Cmp(new(big.Int).SetUint64(SourceChainID)) != 0 {
			return PriceObservation{}, errors.New("Arbitrum RPC chain ID mismatch")
		}
	}
	firstFinalized, err := reader.first.HeaderByNumber(ctx, big.NewInt(-3))
	if err != nil || !validHeader(firstFinalized) {
		return PriceObservation{}, errors.New("read first Arbitrum finalized head")
	}
	secondFinalized, err := reader.second.HeaderByNumber(ctx, big.NewInt(-3))
	if err != nil || !validHeader(secondFinalized) {
		return PriceObservation{}, errors.New("read second Arbitrum finalized head")
	}
	commonNumber := new(big.Int).Set(firstFinalized.Number)
	if secondFinalized.Number.Cmp(commonNumber) < 0 {
		commonNumber.Set(secondFinalized.Number)
	}
	firstHeader, err := reader.first.HeaderByNumber(ctx, commonNumber)
	if err != nil || !validHeader(firstHeader) {
		return PriceObservation{}, errors.New("read first Arbitrum common finalized block")
	}
	secondHeader, err := reader.second.HeaderByNumber(ctx, commonNumber)
	if err != nil || !validHeader(secondHeader) {
		return PriceObservation{}, errors.New("read second Arbitrum common finalized block")
	}
	if firstHeader.Hash() != secondHeader.Hash() || firstHeader.Time != secondHeader.Time {
		return PriceObservation{}, errors.New("Arbitrum finalized RPC disagreement")
	}
	now := reader.clock().UTC()
	blockTime := time.Unix(int64(firstHeader.Time), 0)
	if blockTime.After(now.Add(5*time.Second)) || now.Sub(blockTime) > reader.maxBlockAge {
		return PriceObservation{}, errors.New("Arbitrum common finalized block is stale")
	}
	for _, chain := range []Chain{reader.first, reader.second} {
		code, err := chain.CodeAt(ctx, reader.feed, commonNumber)
		if err != nil || len(code) == 0 || crypto.Keccak256Hash(code) != reader.codeHash {
			return PriceObservation{}, errors.New("AAPL source runtime code mismatch")
		}
	}
	first, err := reader.read(ctx, reader.first, commonNumber, firstHeader)
	if err != nil {
		return PriceObservation{}, err
	}
	second, err := reader.read(ctx, reader.second, commonNumber, secondHeader)
	if err != nil {
		return PriceObservation{}, err
	}
	if !first.Equal(second) {
		return PriceObservation{}, errors.New("AAPL source RPC response disagreement")
	}
	return first, nil
}

func (reader *SourceReader) read(
	ctx context.Context,
	chain Chain,
	block *big.Int,
	header *types.Header,
) (PriceObservation, error) {
	decimals, err := reader.call(ctx, chain, block, "decimals")
	if err != nil || len(decimals) != 1 || !uintValueEquals(decimals[0], uint64(SourceDecimals)) {
		return PriceObservation{}, errors.New("AAPL source decimals mismatch")
	}
	description, err := reader.call(ctx, chain, block, "description")
	if err != nil || len(description) != 1 || description[0] != "AAPL / USD" {
		return PriceObservation{}, errors.New("AAPL source description mismatch")
	}
	values, err := reader.call(ctx, chain, block, "latestRoundData")
	if err != nil || len(values) != 5 {
		return PriceObservation{}, errors.New("read AAPL source round")
	}
	round, roundOK := values[0].(*big.Int)
	answer, answerOK := values[1].(*big.Int)
	started, startedOK := values[2].(*big.Int)
	updated, updatedOK := values[3].(*big.Int)
	answered, answeredOK := values[4].(*big.Int)
	if !roundOK || !answerOK || !startedOK || !updatedOK || !answeredOK || round.Sign() <= 0 ||
		answer.Sign() <= 0 || answer.BitLen() > 191 || !started.IsUint64() || !updated.IsUint64() ||
		started.Sign() <= 0 || started.Cmp(updated) > 0 || answered.Cmp(round) < 0 ||
		round.BitLen() > 80 || answered.BitLen() > 80 {
		return PriceObservation{}, errors.New("invalid AAPL source round")
	}
	updatedAt := updated.Uint64()
	now := reader.clock().UTC()
	if updatedAt == 0 || updatedAt > uint64(now.Add(5*time.Second).Unix()) ||
		updatedAt > header.Time || now.Sub(time.Unix(int64(updatedAt), 0)) > MaxSourceAge {
		return PriceObservation{}, errors.New("AAPL source round is stale")
	}
	return PriceObservation{
		RoundID: new(big.Int).Set(round), Answer: new(big.Int).Set(answer),
		StartedAt: started.Uint64(), UpdatedAt: updatedAt,
		AnsweredInRound: new(big.Int).Set(answered), BlockNumber: block.Uint64(),
		BlockHash: header.Hash(), ObservedAt: now,
	}, nil
}

func (reader *SourceReader) call(ctx context.Context, chain Chain, block *big.Int, method string) ([]any, error) {
	data, err := reader.contract.Pack(method)
	if err != nil {
		return nil, err
	}
	result, err := chain.CallContract(ctx, ethereum.CallMsg{To: &reader.feed, Data: data}, block)
	if err != nil {
		return nil, err
	}
	return reader.contract.Unpack(method, result)
}

type RelayReport struct {
	Sequence        uint64
	RelayedAt       uint64
	SourceUpdatedAt uint64
	SourceRoundID   *big.Int
	AnsweredInRound *big.Int
	Answer          *big.Int
}

type TargetFeed struct {
	address  common.Address
	codeHash common.Hash
	contract abi.ABI
}

func NewTargetFeed(config Config) (*TargetFeed, error) {
	contract, err := abi.JSON(strings.NewReader(targetABIJSON))
	if err != nil {
		return nil, errors.New("parse AAPL relay ABI")
	}
	return &TargetFeed{address: config.TargetFeed, codeHash: config.TargetCodeHash, contract: contract}, nil
}

func (feed *TargetFeed) Verify(ctx context.Context, chain Chain, signer common.Address) (RelayReport, error) {
	id, err := chain.ChainID(ctx)
	if err != nil || id.Cmp(new(big.Int).SetUint64(TargetChainID)) != 0 {
		return RelayReport{}, errors.New("Robinhood RPC chain ID mismatch")
	}
	head, err := chain.HeaderByNumber(ctx, nil)
	if err != nil || !validHeader(head) {
		return RelayReport{}, errors.New("read Robinhood head")
	}
	code, err := chain.CodeAt(ctx, feed.address, head.Number)
	if err != nil || len(code) == 0 || crypto.Keccak256Hash(code) != feed.codeHash {
		return RelayReport{}, errors.New("AAPL relay runtime code mismatch")
	}
	publishers := make([]common.Address, 0, 3)
	for _, method := range []string{"publisher1", "publisher2", "publisher3"} {
		values, err := feed.call(ctx, chain, method)
		if err != nil || len(values) != 1 {
			return RelayReport{}, fmt.Errorf("read AAPL relay %s", method)
		}
		publisher, ok := values[0].(common.Address)
		if !ok || publisher == (common.Address{}) {
			return RelayReport{}, fmt.Errorf("invalid AAPL relay %s", method)
		}
		publishers = append(publishers, publisher)
	}
	matches := 0
	for _, publisher := range publishers {
		if publisher == signer {
			matches++
		}
	}
	if matches != 1 || publishers[0] == publishers[1] || publishers[0] == publishers[2] || publishers[1] == publishers[2] {
		return RelayReport{}, errors.New("publisher key is not uniquely bound to AAPL relay")
	}
	expected := map[string]uint64{
		"publisherCount": 3, "quorum": 2, "maxReportAge": uint64(MaxReportAge.Seconds()),
		"maxSourceAge": uint64(MaxSourceAge.Seconds()), "decimals": uint64(SourceDecimals),
	}
	for method, want := range expected {
		values, err := feed.call(ctx, chain, method)
		if err != nil || len(values) != 1 || !uintValueEquals(values[0], want) {
			return RelayReport{}, fmt.Errorf("AAPL relay %s mismatch", method)
		}
	}
	return feed.ReadReport(ctx, chain, signer)
}

func (feed *TargetFeed) ReadReport(ctx context.Context, chain Chain, signer common.Address) (RelayReport, error) {
	values, err := feed.call(ctx, chain, "reports", signer)
	if err != nil || len(values) != 6 {
		return RelayReport{}, errors.New("read AAPL publisher report")
	}
	sequence, sequenceOK := values[0].(uint64)
	relayedAt, relayedOK := values[1].(uint64)
	updatedAt, updatedOK := values[2].(uint64)
	round, roundOK := values[3].(*big.Int)
	answered, answeredOK := values[4].(*big.Int)
	answer, answerOK := values[5].(*big.Int)
	if !sequenceOK || !relayedOK || !updatedOK || !roundOK || !answeredOK || !answerOK {
		return RelayReport{}, errors.New("decode AAPL publisher report")
	}
	return RelayReport{
		Sequence: sequence, RelayedAt: relayedAt, SourceUpdatedAt: updatedAt,
		SourceRoundID: round, AnsweredInRound: answered, Answer: answer,
	}, nil
}

func (feed *TargetFeed) PackReport(sequence uint64, observation PriceObservation) ([]byte, error) {
	if sequence == 0 || observation.RoundID == nil || observation.Answer == nil ||
		observation.AnsweredInRound == nil || observation.UpdatedAt == 0 {
		return nil, errors.New("invalid AAPL relay report")
	}
	return feed.contract.Pack(
		"report", sequence, observation.RoundID, observation.Answer,
		observation.UpdatedAt, observation.AnsweredInRound,
	)
}

func (feed *TargetFeed) call(ctx context.Context, chain Chain, method string, args ...any) ([]any, error) {
	data, err := feed.contract.Pack(method, args...)
	if err != nil {
		return nil, err
	}
	result, err := chain.CallContract(ctx, ethereum.CallMsg{To: &feed.address, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	return feed.contract.Unpack(method, result)
}

func validHeader(header *types.Header) bool {
	return header != nil && header.Number != nil && header.Number.Sign() >= 0 && header.Time != 0
}

func uintValueEquals(value any, expected uint64) bool {
	switch typed := value.(type) {
	case uint8:
		return uint64(typed) == expected
	case uint64:
		return typed == expected
	case *big.Int:
		return typed.IsUint64() && typed.Uint64() == expected
	default:
		return false
	}
}
