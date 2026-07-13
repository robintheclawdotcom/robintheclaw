package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const lighterPublisherPath = "/v1/publisher/account-state"

type LighterClient struct {
	bridge *SignedClient
}

type lighterBridgeRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
}

type lighterBridgeResponse struct {
	ExecutionAccountID           string    `json:"executionAccountId"`
	AccountIndex                 uint64    `json:"accountIndex"`
	APIKeyIndex                  uint8     `json:"apiKeyIndex"`
	MarketID                     uint16    `json:"marketId"`
	CredentialVersion            int64     `json:"credentialVersion"`
	Nonce                        uint64    `json:"nonce"`
	ExpectedNonce                uint64    `json:"expectedNonce"`
	CollateralRaw                string    `json:"collateralRaw"`
	MaintenanceRequirementRaw    string    `json:"maintenanceRequirementRaw"`
	MaintenanceMarginRatioMicros uint64    `json:"maintenanceMarginRatioMicros"`
	NoUnknownOrders              bool      `json:"noUnknownOrders"`
	NoUnknownPositions           bool      `json:"noUnknownPositions"`
	Flat                         bool      `json:"flat"`
	RESTReconstructed            bool      `json:"restReconstructed"`
	TradeCount                   int       `json:"tradeCount"`
	LastTradeID                  uint64    `json:"lastTradeId"`
	StateDigest                  string    `json:"stateDigest"`
	ObservedAt                   time.Time `json:"observedAt"`
}

func NewLighterClient(endpoint EndpointConfig, client *http.Client) (*LighterClient, error) {
	bridge, err := NewSignedClient(endpoint.URL, endpoint.Caller, endpoint.HMACKey, client)
	if err != nil {
		return nil, fmt.Errorf("configure Lighter publisher bridge: %w", err)
	}
	return &LighterClient{bridge: bridge}, nil
}

func (value *LighterClient) Collect(ctx context.Context, executionID string, binding LighterBinding) (LighterObservation, error) {
	if !validExecutionID(executionID) || binding.AccountIndex == 0 || binding.APIKeyIndex < 2 || binding.APIKeyIndex > 254 {
		return LighterObservation{}, errors.New("invalid Lighter binding")
	}
	body, err := json.Marshal(lighterBridgeRequest{ExecutionAccountID: executionID})
	if err != nil {
		return LighterObservation{}, err
	}
	var response lighterBridgeResponse
	if err := value.bridge.Call(ctx, lighterPublisherPath, body, &response); err != nil {
		return LighterObservation{}, err
	}
	if response.ExecutionAccountID != executionID || response.AccountIndex != binding.AccountIndex ||
		response.APIKeyIndex != binding.APIKeyIndex || response.MarketID != binding.MarketID || response.CredentialVersion < 1 {
		return LighterObservation{}, errors.New("Lighter publisher bridge identity mismatch")
	}
	if response.TradeCount < 0 || response.StateDigest == "" || !fresh(response.ObservedAt, time.Now()) {
		return LighterObservation{}, errors.New("Lighter publisher bridge returned invalid evidence")
	}
	if _, err := parseUnsignedDecimal(response.CollateralRaw); err != nil {
		return LighterObservation{}, errors.New("Lighter publisher bridge returned invalid collateral")
	}
	if _, err := parseUnsignedDecimal(response.MaintenanceRequirementRaw); err != nil {
		return LighterObservation{}, errors.New("Lighter publisher bridge returned invalid maintenance margin")
	}
	return LighterObservation{
		AccountIndex: response.AccountIndex, APIKeyIndex: response.APIKeyIndex,
		Nonce: response.Nonce, ExpectedNonce: response.ExpectedNonce,
		CollateralRaw: response.CollateralRaw, MaintenanceRequirementRaw: response.MaintenanceRequirementRaw,
		MaintenanceMarginRatioMicros: response.MaintenanceMarginRatioMicros,
		NoUnknownOrders:              response.NoUnknownOrders, NoUnknownPositions: response.NoUnknownPositions,
		CollateralReady: decimalAtLeast(response.CollateralRaw, binding.MinimumCollateralRaw),
		Flat:            response.Flat, RESTReconstructed: response.RESTReconstructed,
		TradeCount: response.TradeCount, LastTradeID: response.LastTradeID,
		StateDigest: response.StateDigest, ObservedAt: response.ObservedAt,
	}, nil
}
