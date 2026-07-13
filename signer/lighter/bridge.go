package main

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
	"strings"
	"time"
)

type signingBridge interface {
	call(context.Context, string, any, any) error
}

type httpBridge struct {
	baseURL string
	caller  string
	key     []byte
	http    *http.Client
	now     func() time.Time
	random  io.Reader
}

type bridgeResponseError struct {
	status int
}

func (value *bridgeResponseError) Error() string {
	return fmt.Sprintf("signing bridge returned status %d", value.status)
}

func newHTTPBridge(baseURL, caller string, key []byte) *httpBridge {
	return &httpBridge{
		baseURL: strings.TrimRight(baseURL, "/"),
		caller:  caller,
		key:     append([]byte(nil), key...),
		http: &http.Client{
			Timeout: 8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("signing bridge redirect refused")
			},
		},
		now:    time.Now,
		random: rand.Reader,
	}
}

func (value *httpBridge) call(ctx context.Context, path string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("encode signing request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, value.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("construct signing request")
	}
	nonceBytes := make([]byte, 24)
	if _, err := io.ReadFull(value.random, nonceBytes); err != nil {
		return errors.New("generate signing nonce")
	}
	nonce := hex.EncodeToString(nonceBytes)
	timestamp := fmt.Sprintf("%d", value.now().Unix())
	digest := sha256.Sum256(body)
	canonical := fmt.Sprintf("POST\n%s\n%s\n%s\n%s\n%x", path, value.caller, timestamp, nonce, digest)
	mac := hmac.New(sha256.New, value.key)
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-RTC-Caller", value.caller)
	request.Header.Set("X-RTC-Timestamp", timestamp)
	request.Header.Set("X-RTC-Nonce", nonce)
	request.Header.Set("X-RTC-Signature", hex.EncodeToString(mac.Sum(nil)))

	response, err := value.http.Do(request)
	if err != nil {
		return errors.New("signing bridge unavailable")
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil || len(responseBody) > maxBodyBytes {
		return errors.New("invalid signing response")
	}
	provided, err := hex.DecodeString(response.Header.Get("X-RTC-Response-Signature"))
	if err != nil || len(provided) != sha256.Size {
		return errors.New("unauthenticated signing response")
	}
	responseDigest := sha256.Sum256(responseBody)
	responseCanonical := fmt.Sprintf(
		"RESPONSE\n%s\n%s\n%s\n%d\n%x",
		path,
		value.caller,
		nonce,
		response.StatusCode,
		responseDigest,
	)
	responseMAC := hmac.New(sha256.New, value.key)
	_, _ = responseMAC.Write([]byte(responseCanonical))
	if subtle.ConstantTimeCompare(responseMAC.Sum(nil), provided) != 1 {
		return errors.New("unauthenticated signing response")
	}
	if response.StatusCode != http.StatusOK {
		return &bridgeResponseError{status: response.StatusCode}
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return errors.New("invalid signing response")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid signing response")
	}
	return nil
}
