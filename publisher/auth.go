package publisher

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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

func NewSignedClient(baseURL, caller, keyFile string, client *http.Client) (*SignedClient, error) {
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
	keyBytes, err := readSecretFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read HMAC key: %w", err)
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(keyBytes)))
	if err != nil || len(decoded) != 32 || strings.TrimSpace(string(keyBytes)) != strings.ToLower(strings.TrimSpace(string(keyBytes))) {
		return nil, errors.New("HMAC key must be 32-byte lowercase hex")
	}
	var key [32]byte
	copy(key[:], decoded)
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
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

func readSecretFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("secret file must not be accessible by group or other")
	}
	return os.ReadFile(path)
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
