package strategyrunner

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maximumCoordinatorResponseBytes = 64 << 10

var (
	ErrCoordinatorAmbiguous       = errors.New("coordinator persistence is ambiguous")
	ErrCoordinatorDeclined        = errors.New("coordinator declined intent")
	ErrCoordinatorNotPersisted    = errors.New("coordinator did not persist intent")
	ErrCoordinatorPayloadConflict = errors.New("coordinator payload identity conflicts")
)

type IntentPersistence struct {
	Status             string `json:"status"`
	IntentID           string `json:"intent_id"`
	CoordinatorState   string `json:"coordinator_state"`
	CoordinatorVersion uint64 `json:"coordinator_version"`
}

type IntentDispatcher interface {
	SubmitIntent(context.Context, PairIntent) (IntentPersistence, error)
}

type CoordinatorClient struct {
	endpoint string
	caller   string
	key      []byte
	client   *http.Client
	now      func() time.Time
	nonce    func() (string, error)
}

type coordinatorSaga struct {
	IntentID        string `json:"intent_id"`
	State           string `json:"state"`
	Version         uint64 `json:"version"`
	PerpFilledBase  uint64 `json:"perp_filled_base"`
	PerpUnwoundBase uint64 `json:"perp_unwound_base"`
	SpotReceivedRaw string `json:"spot_received_raw"`
}

type intentStatusRequest struct {
	IntentID      string `json:"intent_id"`
	PayloadSHA256 string `json:"payload_sha256"`
}

type intentStatusResponse struct {
	IntentID      string           `json:"intent_id"`
	PayloadSHA256 string           `json:"payload_sha256"`
	Status        string           `json:"status"`
	Saga          *coordinatorSaga `json:"saga"`
}

func NewCoordinatorClient(baseURL, caller string, key []byte) (*CoordinatorClient, error) {
	endpoint, err := coordinatorEndpoint(baseURL)
	if err != nil {
		return nil, err
	}
	if !validCaller(caller) || len(key) != sha256.Size {
		return nil, errors.New("coordinator caller and 32-byte HMAC key are required")
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   2 * time.Second,
		ResponseHeaderTimeout: 2 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &CoordinatorClient{
		endpoint: endpoint,
		caller:   caller,
		key:      append([]byte(nil), key...),
		client: &http.Client{
			Transport: transport,
			Timeout:   3 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now:   time.Now,
		nonce: randomNonce,
	}, nil
}

func (c *CoordinatorClient) SubmitIntent(ctx context.Context, intent PairIntent) (IntentPersistence, error) {
	body, err := json.Marshal(intent)
	if err != nil {
		return IntentPersistence{}, fmt.Errorf("encode coordinator intent: %w", err)
	}
	payloadDigest := sha256.Sum256(body)
	payloadSHA256 := hex.EncodeToString(payloadDigest[:])
	persistence, err := c.submitIntent(ctx, intent, body)
	if err == nil || !errors.Is(err, ErrCoordinatorAmbiguous) {
		return persistence, err
	}
	return c.resolveIntent(ctx, intent, payloadSHA256)
}

func (c *CoordinatorClient) submitIntent(ctx context.Context, intent PairIntent, body []byte) (IntentPersistence, error) {
	response, responseBody, err := c.post(ctx, "/v1/intents", body)
	if err != nil {
		return IntentPersistence{}, err
	}
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return IntentPersistence{}, fmt.Errorf("%w with status %d", ErrCoordinatorDeclined, response.StatusCode)
		}
		return IntentPersistence{}, fmt.Errorf("%w: coordinator returned status %d", ErrCoordinatorAmbiguous, response.StatusCode)
	}
	var saga coordinatorSaga
	if err := decodeStrict(response, responseBody, &saga); err != nil {
		return IntentPersistence{}, err
	}
	return persistenceFromSaga(intent.ID, &saga, response.StatusCode == http.StatusCreated)
}

func (c *CoordinatorClient) resolveIntent(ctx context.Context, intent PairIntent, payloadSHA256 string) (IntentPersistence, error) {
	body, err := json.Marshal(intentStatusRequest{IntentID: intent.ID, PayloadSHA256: payloadSHA256})
	if err != nil {
		return IntentPersistence{}, fmt.Errorf("encode intent status request: %w", err)
	}
	response, responseBody, err := c.post(ctx, "/v1/intent-status", body)
	if err != nil {
		return IntentPersistence{}, err
	}
	if response.StatusCode != http.StatusOK {
		return IntentPersistence{}, fmt.Errorf("%w: intent status returned %d", ErrCoordinatorAmbiguous, response.StatusCode)
	}
	var status intentStatusResponse
	if err := decodeStrict(response, responseBody, &status); err != nil {
		return IntentPersistence{}, err
	}
	if status.IntentID != intent.ID || status.PayloadSHA256 != payloadSHA256 {
		return IntentPersistence{}, fmt.Errorf("%w: intent status identity mismatch", ErrCoordinatorAmbiguous)
	}
	switch status.Status {
	case "persisted":
		if status.Saga == nil {
			return IntentPersistence{}, fmt.Errorf("%w: persisted status has no saga", ErrCoordinatorAmbiguous)
		}
		return persistenceFromSaga(intent.ID, status.Saga, false)
	case "absent":
		if status.Saga != nil {
			return IntentPersistence{}, fmt.Errorf("%w: absent status contains a saga", ErrCoordinatorAmbiguous)
		}
		return IntentPersistence{}, ErrCoordinatorNotPersisted
	case "conflict", "unverifiable":
		if status.Saga != nil {
			return IntentPersistence{}, fmt.Errorf("%w: conflict status contains a saga", ErrCoordinatorAmbiguous)
		}
		return IntentPersistence{}, ErrCoordinatorPayloadConflict
	default:
		return IntentPersistence{}, fmt.Errorf("%w: unknown intent status", ErrCoordinatorAmbiguous)
	}
}

func (c *CoordinatorClient) post(ctx context.Context, path string, body []byte) (*http.Response, []byte, error) {
	nonce, err := c.nonce()
	if err != nil || !validNonce(nonce) {
		return nil, nil, errors.New("coordinator nonce unavailable")
	}
	timestamp := strconv.FormatInt(c.now().Unix(), 10)
	signature := coordinatorMAC(c.key, path, c.caller, timestamp, nonce, body)
	endpoint := strings.TrimSuffix(c.endpoint, "/v1/intents") + path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-RTC-Caller", c.caller)
	request.Header.Set("X-RTC-Timestamp", timestamp)
	request.Header.Set("X-RTC-Nonce", nonce)
	request.Header.Set("X-RTC-Signature", hex.EncodeToString(signature))

	response, err := c.client.Do(request)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrCoordinatorAmbiguous, err)
	}
	defer response.Body.Close()
	responseBody, err := readBounded(response.Body, maximumCoordinatorResponseBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: invalid response body", ErrCoordinatorAmbiguous)
	}
	return response, responseBody, nil
}

func decodeStrict(response *http.Response, responseBody []byte, target any) error {
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("%w: invalid response content type", ErrCoordinatorAmbiguous)
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("%w: invalid response schema", ErrCoordinatorAmbiguous)
	}
	return nil
}

func persistenceFromSaga(intentID string, saga *coordinatorSaga, requirePrechecked bool) (IntentPersistence, error) {
	if saga.IntentID != intentID || saga.Version == 0 || !persistedSagaState(saga.State) {
		return IntentPersistence{}, fmt.Errorf("%w: response does not prove intent persistence", ErrCoordinatorAmbiguous)
	}
	if requirePrechecked && (saga.State != "prechecked" || saga.Version != 1 || saga.PerpFilledBase != 0 ||
		saga.PerpUnwoundBase != 0 || saga.SpotReceivedRaw != "0") {
		return IntentPersistence{}, fmt.Errorf("%w: new intent response is not prechecked", ErrCoordinatorAmbiguous)
	}
	return IntentPersistence{
		Status:             "persisted",
		IntentID:           saga.IntentID,
		CoordinatorState:   saga.State,
		CoordinatorVersion: saga.Version,
	}, nil
}

func persistedSagaState(state string) bool {
	switch state {
	case "prechecked", "perp_submitted", "perp_partial", "perp_filled", "spot_submitted", "hedged",
		"exiting", "unwinding", "closed", "cancelled", "expired", "unhedged", "failed_safe":
		return true
	default:
		return false
	}
}

func coordinatorEndpoint(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("coordinator URL is invalid")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && privateHost(parsed.Hostname())) {
		return "", errors.New("coordinator URL must use HTTPS or private-network HTTP")
	}
	parsed.Path = "/v1/intents"
	return parsed.String(), nil
}

func privateHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".internal") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && (address.IsLoopback() || address.IsPrivate())
}

func coordinatorMAC(key []byte, path, caller, timestamp, nonce string, body []byte) []byte {
	digest := sha256.Sum256(body)
	canonical := fmt.Sprintf("POST\n%s\n%s\n%s\n%s\n%s", path, caller, timestamp, nonce, hex.EncodeToString(digest[:]))
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	return mac.Sum(nil)
}

func randomNonce() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func validNonce(value string) bool {
	if len(value) < 32 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return false
		}
	}
	return true
}

func validCaller(value string) bool {
	if len(value) < 3 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	limited := io.LimitReader(reader, maximum+1)
	body, err := io.ReadAll(limited)
	if err != nil || int64(len(body)) > maximum {
		return nil, errors.New("response body exceeds limit")
	}
	return body, nil
}
