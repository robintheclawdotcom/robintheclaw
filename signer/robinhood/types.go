package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

type SpotSide uint8

const (
	BuySpot SpotSide = iota
	SellSpot
)

type SpotIntentRequest struct {
	ID                   string `json:"id"`
	StockToken           string `json:"stock_token"`
	Side                 string `json:"side"`
	AmountIn             string `json:"amount_in"`
	MinAmountOut         string `json:"min_amount_out"`
	ExpectedUIMultiplier string `json:"expected_ui_multiplier"`
	MinOracleRoundID     string `json:"min_oracle_round_id"`
	Deadline             uint64 `json:"deadline"`
	ConfigVersion        uint64 `json:"config_version"`
}

type ExecuteRequest struct {
	ExecutionAccountID string            `json:"execution_account_id"`
	RequestID          string            `json:"request_id"`
	ReplacesRequestID  string            `json:"replaces_request_id,omitempty"`
	Intent             SpotIntentRequest `json:"intent"`
}

type SpotIntent struct {
	ID                   [32]byte
	StockToken           common.Address
	Side                 SpotSide
	AmountIn             *big.Int
	MinAmountOut         *big.Int
	ExpectedUIMultiplier *big.Int
	MinOracleRoundID     *big.Int
	Deadline             uint64
	ConfigVersion        uint64
}

type SubmissionStatus string

const (
	SubmissionSigned        SubmissionStatus = "signed"
	SubmissionSubmitted     SubmissionStatus = "submitted"
	SubmissionSoftConfirmed SubmissionStatus = "soft_confirmed"
	SubmissionL1Posted      SubmissionStatus = "l1_posted"
	SubmissionEthereumFinal SubmissionStatus = "ethereum_final"
	SubmissionReverted      SubmissionStatus = "reverted"
	SubmissionAmbiguous     SubmissionStatus = "ambiguous"
	SubmissionReplaced      SubmissionStatus = "replaced"
	SubmissionSuperseded    SubmissionStatus = "superseded"
	SubmissionQuarantined   SubmissionStatus = "quarantined"
)

type Submission struct {
	ExecutionAccountID string           `json:"execution_account_id"`
	VaultAddress       string           `json:"vault_address"`
	SignerAddress      string           `json:"signer_address"`
	RequestID          string           `json:"request_id"`
	IntentID           string           `json:"intent_id"`
	TxHash             string           `json:"tx_hash"`
	Nonce              uint64           `json:"nonce"`
	Status             SubmissionStatus `json:"status"`
}

var errWriterNotReady = errors.New("writer is not ready")

type journaledSubmissionError struct {
	submission Submission
	cause      error
}

func (failure *journaledSubmissionError) Error() string {
	return fmt.Sprintf("journaled submission requires reconciliation: %v", failure.cause)
}

func (failure *journaledSubmissionError) Unwrap() error {
	return failure.cause
}

func (failure *journaledSubmissionError) Submission() Submission {
	return failure.submission
}

func (request ExecuteRequest) validate() (SpotIntent, []byte, string, error) {
	if !validExecutionAccountID(request.ExecutionAccountID) {
		return SpotIntent{}, nil, "", errors.New("invalid execution_account_id")
	}
	if strings.TrimSpace(request.RequestID) == "" || len(request.RequestID) > 128 {
		return SpotIntent{}, nil, "", errors.New("invalid request_id")
	}
	if request.ReplacesRequestID == request.RequestID || len(request.ReplacesRequestID) > 128 {
		return SpotIntent{}, nil, "", errors.New("invalid replaces_request_id")
	}
	intent, err := request.Intent.parse()
	if err != nil {
		return SpotIntent{}, nil, "", err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return SpotIntent{}, nil, "", errors.New("encode request")
	}
	digest := sha256.Sum256(payload)
	return intent, payload, fmt.Sprintf("%x", digest), nil
}

func (request SpotIntentRequest) parse() (SpotIntent, error) {
	var intent SpotIntent
	id := common.FromHex(request.ID)
	if len(id) != len(intent.ID) || common.BytesToHash(id) == (common.Hash{}) {
		return intent, errors.New("intent id must be a non-zero bytes32")
	}
	copy(intent.ID[:], id)
	if !common.IsHexAddress(request.StockToken) || common.HexToAddress(request.StockToken) == (common.Address{}) {
		return intent, errors.New("stock_token must be a non-zero address")
	}
	intent.StockToken = common.HexToAddress(request.StockToken)
	switch request.Side {
	case "buy_spot":
		intent.Side = BuySpot
	case "sell_spot":
		intent.Side = SellSpot
	default:
		return intent, errors.New("side must be buy_spot or sell_spot")
	}
	amountIn, err := parseUint(request.AmountIn, 128)
	if err != nil || amountIn.Sign() == 0 {
		return intent, errors.New("amount_in must be a positive uint128")
	}
	minimum, err := parseUint(request.MinAmountOut, 128)
	if err != nil || minimum.Sign() == 0 {
		return intent, errors.New("min_amount_out must be a positive uint128")
	}
	multiplier, err := parseUint(request.ExpectedUIMultiplier, 256)
	if err != nil || multiplier.Sign() == 0 {
		return intent, errors.New("expected_ui_multiplier must be a positive uint256")
	}
	minimumRound, err := parseUint(request.MinOracleRoundID, 80)
	if err != nil || minimumRound.Sign() == 0 {
		return intent, errors.New("min_oracle_round_id must be a positive uint80")
	}
	if request.Deadline == 0 || request.ConfigVersion == 0 {
		return intent, errors.New("deadline and config_version must be positive")
	}
	intent.AmountIn = amountIn
	intent.MinAmountOut = minimum
	intent.ExpectedUIMultiplier = multiplier
	intent.MinOracleRoundID = minimumRound
	intent.Deadline = request.Deadline
	intent.ConfigVersion = request.ConfigVersion
	return intent, nil
}

func parseUint(value string, bits int) (*big.Int, error) {
	if value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(value, "+") ||
		(len(value) > 1 && value[0] == '0') {
		return nil, errors.New("invalid integer")
	}
	number, ok := new(big.Int).SetString(value, 10)
	if !ok || number.Sign() < 0 || number.BitLen() > bits {
		return nil, fmt.Errorf("outside uint%d", bits)
	}
	return number, nil
}
