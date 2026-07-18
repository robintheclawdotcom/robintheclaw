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
	client               *http.Client
	base                 *url.URL
	path                 string
	caller               string
	key                  []byte
	authenticateResponse bool
}

func NewQuoteClient(client *http.Client, rawURL, caller string, key []byte) (QuoteClient, error) {
	return newSignedClient(client, rawURL, "/v1/executable-quotes", caller, key, false)
}

func NewRunnerClient(client *http.Client, rawURL, caller string, key []byte) (RunnerClient, error) {
	return newSignedClient(client, rawURL, "/v1/run", caller, key, true)
}

func newSignedClient(client *http.Client, rawURL, path, caller string, key []byte, authenticateResponse bool) (*signedClient, error) {
	parsed, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || caller == "" || len(key) < 32 {
		return nil, fmt.Errorf("invalid signed client configuration")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &signedClient{
		client: client, base: parsed, path: path, caller: caller, key: append([]byte(nil), key...),
		authenticateResponse: authenticateResponse,
	}, nil
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
	responseBody, err := readBoundedResponse(response.Body, 256<<10)
	if err != nil {
		return nil, fmt.Errorf("upstream response exceeds limit")
	}
	if c.authenticateResponse {
		signature, err := hex.DecodeString(response.Header.Get("X-Robin-Response-Signature"))
		if err != nil || len(signature) != sha256.Size ||
			!hmac.Equal(signature, responseMAC(c.key, c.path, c.caller, nonce, response.StatusCode, responseBody)) {
			return nil, fmt.Errorf("strategy runner response authentication failed")
		}
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

func responseMAC(key []byte, path, caller, nonce string, status int, body []byte) []byte {
	bodyHash := sha256.Sum256(body)
	canonical := fmt.Sprintf("RESPONSE\n%s\n%s\n%s\n%d\n%s", path, caller, nonce, status, hex.EncodeToString(bodyHash[:]))
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	return mac.Sum(nil)
}

func readBoundedResponse(reader io.Reader, maximum int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || int64(len(body)) > maximum {
		return nil, fmt.Errorf("response body exceeds limit")
	}
	return body, nil
}
