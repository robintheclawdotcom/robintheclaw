package quoteauthority

import (
	"bytes"
	"context"
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

	"github.com/robin-the-claw/liveexec/protocol"
)

const maximumCoordinatorResponseBytes = 64 << 10

var (
	ErrMarketQuoteAmbiguous = errors.New("market quote persistence is ambiguous")
	ErrMarketQuoteConflict  = errors.New("market quote identity conflicts")
)

type CoordinatorPublisher struct {
	endpoint string
	caller   string
	key      []byte
	client   *http.Client
	now      func() time.Time
	nonce    func() (string, error)
}

func NewCoordinatorPublisher(baseURL, caller string, key []byte) (*CoordinatorPublisher, error) {
	endpoint, err := coordinatorEndpoint(baseURL, "/v1/market-quotes")
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
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   2 * time.Second,
		ResponseHeaderTimeout: 2 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &CoordinatorPublisher{
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

func (p *CoordinatorPublisher) Publish(ctx context.Context, quote protocol.MarketQuotePublication) (protocol.MarketQuoteReceipt, error) {
	body, err := json.Marshal(quote)
	if err != nil {
		return protocol.MarketQuoteReceipt{}, fmt.Errorf("encode market quote: %w", err)
	}
	digest := sha256.Sum256(body)
	payloadSHA256 := hex.EncodeToString(digest[:])
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		receipt, err := p.publish(ctx, body, payloadSHA256)
		if err == nil || errors.Is(err, ErrMarketQuoteConflict) {
			return receipt, err
		}
		last = err
	}
	return protocol.MarketQuoteReceipt{}, last
}

func (p *CoordinatorPublisher) publish(ctx context.Context, body []byte, payloadSHA256 string) (protocol.MarketQuoteReceipt, error) {
	nonce, err := p.nonce()
	if err != nil || !validNonce(nonce) {
		return protocol.MarketQuoteReceipt{}, errors.New("coordinator nonce unavailable")
	}
	timestamp := strconv.FormatInt(p.now().Unix(), 10)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return protocol.MarketQuoteReceipt{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-RTC-Caller", p.caller)
	request.Header.Set("X-RTC-Timestamp", timestamp)
	request.Header.Set("X-RTC-Nonce", nonce)
	request.Header.Set("X-RTC-Signature", hex.EncodeToString(protocol.RequestMAC(
		p.key, http.MethodPost, "/v1/market-quotes", p.caller, timestamp, nonce, body,
	)))
	response, err := p.client.Do(request)
	if err != nil {
		return protocol.MarketQuoteReceipt{}, fmt.Errorf("%w: %v", ErrMarketQuoteAmbiguous, err)
	}
	defer response.Body.Close()
	responseBody, err := readBounded(response.Body, maximumCoordinatorResponseBytes)
	if err != nil {
		return protocol.MarketQuoteReceipt{}, fmt.Errorf("%w: invalid response body", ErrMarketQuoteAmbiguous)
	}
	if response.StatusCode == http.StatusConflict {
		return protocol.MarketQuoteReceipt{}, ErrMarketQuoteConflict
	}
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		return protocol.MarketQuoteReceipt{}, fmt.Errorf("%w: coordinator returned %d", ErrMarketQuoteAmbiguous, response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return protocol.MarketQuoteReceipt{}, fmt.Errorf("%w: invalid response content type", ErrMarketQuoteAmbiguous)
	}
	var receipt protocol.MarketQuoteReceipt
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return protocol.MarketQuoteReceipt{}, fmt.Errorf("%w: invalid response schema", ErrMarketQuoteAmbiguous)
	}
	wantStatus := "recorded"
	if response.StatusCode == http.StatusOK {
		wantStatus = "duplicate"
	}
	if receipt.Status != wantStatus || receipt.PayloadSHA256 != payloadSHA256 ||
		receipt.SourceSession == "" || receipt.SourceEventID == "" {
		return protocol.MarketQuoteReceipt{}, fmt.Errorf("%w: persistence receipt mismatch", ErrMarketQuoteAmbiguous)
	}
	return receipt, nil
}

func coordinatorEndpoint(baseURL, path string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("coordinator URL is invalid")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && privateHost(parsed.Hostname())) {
		return "", errors.New("coordinator URL must use HTTPS or private-network HTTP")
	}
	parsed.Path = path
	return parsed.String(), nil
}

func privateHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".internal") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && (address.IsLoopback() || address.IsPrivate())
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
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '-' && char != '_' {
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
