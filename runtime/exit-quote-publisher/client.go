package exitquote

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
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

	"github.com/robin-the-claw/liveexec/protocol"
)

const maximumResponseBytes = 256 << 10

type QuoteClient interface {
	Quote(context.Context, protocol.QuoteRequest) (protocol.QuoteBundle, error)
}

type ResponseError struct{ Status int }

func (err *ResponseError) Error() string {
	return fmt.Sprintf("quote authority returned HTTP %d", err.Status)
}

type signedQuoteClient struct {
	client *http.Client
	url    string
	caller string
	key    []byte
}

func NewQuoteClient(client *http.Client, rawURL, caller string, key []byte) (QuoteClient, error) {
	if err := validateServiceURL(rawURL); err != nil {
		return nil, errors.New("invalid quote client configuration")
	}
	parsed, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil || parsed.Host == "" || !accountPattern.MatchString(caller) || len(key) < 32 {
		return nil, errors.New("invalid quote client configuration")
	}
	parsed.Path = "/v1/executable-quotes"
	if client == nil {
		client = http.DefaultClient
	}
	return &signedQuoteClient{client: client, url: parsed.String(), caller: caller, key: append([]byte(nil), key...)}, nil
}

func (client *signedQuoteClient) Quote(ctx context.Context, input protocol.QuoteRequest) (protocol.QuoteBundle, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return protocol.QuoteBundle{}, err
	}
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		return protocol.QuoteBundle{}, err
	}
	nonce := hex.EncodeToString(nonceBytes)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.url, bytes.NewReader(body))
	if err != nil {
		return protocol.QuoteBundle{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Robin-Caller", client.caller)
	request.Header.Set("X-Robin-Timestamp", timestamp)
	request.Header.Set("X-Robin-Nonce", nonce)
	request.Header.Set("X-Robin-Signature", hex.EncodeToString(requestMAC(client.key, http.MethodPost,
		"/v1/executable-quotes", client.caller, timestamp, nonce, body)))
	response, err := client.client.Do(request)
	if err != nil {
		return protocol.QuoteBundle{}, err
	}
	defer response.Body.Close()
	responseBody, err := readBounded(response.Body, maximumResponseBytes)
	if err != nil {
		return protocol.QuoteBundle{}, err
	}
	if response.StatusCode != http.StatusOK {
		return protocol.QuoteBundle{}, &ResponseError{Status: response.StatusCode}
	}
	var quote protocol.QuoteBundle
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&quote); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return protocol.QuoteBundle{}, errors.New("invalid quote response")
	}
	return quote, nil
}

func requestMAC(key []byte, method, path, caller, timestamp, nonce string, body []byte) []byte {
	bodyHash := sha256.Sum256(body)
	canonical := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s", method, path, caller, timestamp, nonce, hex.EncodeToString(bodyHash[:]))
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	return mac.Sum(nil)
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	value, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || int64(len(value)) > maximum {
		return nil, errors.New("quote response exceeds limit")
	}
	return value, nil
}
