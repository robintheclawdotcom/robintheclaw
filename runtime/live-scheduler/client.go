package scheduler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type QuoteClient interface {
	Quote(context.Context, []byte) ([]byte, error)
}

type RunnerClient interface {
	Run(context.Context, []byte) ([]byte, error)
}

type ResponseError struct {
	Status int
	Body   []byte
}

func (e *ResponseError) Error() string { return fmt.Sprintf("upstream returned HTTP %d", e.Status) }

type signedClient struct {
	client *http.Client
	base   *url.URL
	path   string
	caller string
	key    []byte
}

func NewQuoteClient(client *http.Client, rawURL, caller string, key []byte) (QuoteClient, error) {
	return newSignedClient(client, rawURL, "/v1/executable-quotes", caller, key)
}

func NewRunnerClient(client *http.Client, rawURL, caller string, key []byte) (RunnerClient, error) {
	return newSignedClient(client, rawURL, "/v1/run", caller, key)
}

func newSignedClient(client *http.Client, rawURL, path, caller string, key []byte) (*signedClient, error) {
	parsed, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || caller == "" || len(key) < 32 {
		return nil, fmt.Errorf("invalid signed client configuration")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &signedClient{client: client, base: parsed, path: path, caller: caller, key: append([]byte(nil), key...)}, nil
}

func (c *signedClient) Quote(ctx context.Context, body []byte) ([]byte, error) {
	return c.call(ctx, body)
}
func (c *signedClient) Run(ctx context.Context, body []byte) ([]byte, error) {
	return c.call(ctx, body)
}

func (c *signedClient) call(ctx context.Context, body []byte) ([]byte, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		return nil, fmt.Errorf("create request nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.JoinPath(c.path).String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Robin-Caller", c.caller)
	request.Header.Set("X-Robin-Timestamp", timestamp)
	request.Header.Set("X-Robin-Nonce", nonce)
	request.Header.Set("X-Robin-Signature", hex.EncodeToString(requestMAC(c.key, http.MethodPost, c.path, c.caller, timestamp, nonce, body)))
	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 256<<10))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, &ResponseError{Status: response.StatusCode, Body: responseBody}
	}
	return responseBody, nil
}

func requestMAC(key []byte, method, path, caller, timestamp, nonce string, body []byte) []byte {
	bodyHash := sha256.Sum256(body)
	canonical := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s", method, path, caller, timestamp, nonce, hex.EncodeToString(bodyHash[:]))
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	return mac.Sum(nil)
}
