package publisher

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type SignedClient struct {
	baseURL string
	caller  string
	key     [32]byte
	client  *http.Client
}

func NewSignedClient(baseURL, caller, keyHex string, client *http.Client) (*SignedClient, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("invalid signed endpoint URL")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && !strings.HasSuffix(parsed.Hostname(), ".internal") {
		return nil, errors.New("signed endpoint must use HTTPS or a private service host")
	}
	if !validCaller(caller) {
		return nil, errors.New("invalid caller id")
	}
	decoded, err := hex.DecodeString(keyHex)
	if err != nil || len(decoded) != 32 || keyHex != strings.ToLower(keyHex) {
		return nil, errors.New("HMAC key must be 32-byte lowercase hex")
	}
	var key [32]byte
	copy(key[:], decoded)
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	} else {
		clone := *client
		client = &clone
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("signed endpoint redirect refused")
	}
	return &SignedClient{baseURL: strings.TrimRight(baseURL, "/"), caller: caller, key: key, client: client}, nil
}

func (c *SignedClient) Post(ctx context.Context, path string, body []byte) error {
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		return err
	}
	nonce := hex.EncodeToString(nonceBytes)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	digest := sha256.Sum256(body)
	canonical := strings.Join([]string{"POST", path, c.caller, timestamp, nonce, hex.EncodeToString(digest[:])}, "\n")
	mac := hmac.New(sha256.New, c.key[:])
	_, _ = mac.Write([]byte(canonical))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-RTC-Caller", c.caller)
	req.Header.Set("X-RTC-Timestamp", timestamp)
	req.Header.Set("X-RTC-Nonce", nonce)
	req.Header.Set("X-RTC-Signature", hex.EncodeToString(mac.Sum(nil)))
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusMethodNotAllowed {
		return ErrRateLimited
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("signed endpoint returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *SignedClient) Call(ctx context.Context, path string, body []byte, target any) error {
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		return err
	}
	nonce := hex.EncodeToString(nonceBytes)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	digest := sha256.Sum256(body)
	canonical := strings.Join([]string{"POST", path, c.caller, timestamp, nonce, hex.EncodeToString(digest[:])}, "\n")
	mac := hmac.New(sha256.New, c.key[:])
	_, _ = mac.Write([]byte(canonical))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-RTC-Caller", c.caller)
	request.Header.Set("X-RTC-Timestamp", timestamp)
	request.Header.Set("X-RTC-Nonce", nonce)
	request.Header.Set("X-RTC-Signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 64<<10+1))
	if err != nil || len(responseBody) > 64<<10 {
		return errors.New("invalid authenticated response")
	}
	provided, err := hex.DecodeString(response.Header.Get("X-RTC-Response-Signature"))
	if err != nil || len(provided) != sha256.Size {
		return errors.New("unauthenticated response")
	}
	responseDigest := sha256.Sum256(responseBody)
	responseCanonical := strings.Join([]string{
		"RESPONSE", path, c.caller, nonce, strconv.Itoa(response.StatusCode), hex.EncodeToString(responseDigest[:]),
	}, "\n")
	responseMAC := hmac.New(sha256.New, c.key[:])
	_, _ = responseMAC.Write([]byte(responseCanonical))
	if subtle.ConstantTimeCompare(responseMAC.Sum(nil), provided) != 1 {
		return errors.New("unauthenticated response")
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusMethodNotAllowed {
		return ErrRateLimited
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("authenticated endpoint returned status %d", response.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid authenticated response")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid authenticated response")
	}
	return nil
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
