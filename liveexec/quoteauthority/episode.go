package quoteauthority

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
)

var ErrOpenEpisodeUnavailable = errors.New("open episode is unavailable")

type CoordinatorEpisodeResolver struct {
	baseURL string
	caller  string
	key     []byte
	client  *http.Client
	now     func() time.Time
	nonce   func() (string, error)
}

func NewCoordinatorEpisodeResolver(baseURL, caller string, key []byte) (*CoordinatorEpisodeResolver, error) {
	if _, err := coordinatorEndpoint(baseURL, "/v1/open-episodes/placeholder/0x"+strings.Repeat("a", 64)); err != nil {
		return nil, err
	}
	if !validCaller(caller) || len(key) != sha256.Size {
		return nil, errors.New("coordinator episode caller and 32-byte HMAC key are required")
	}
	return &CoordinatorEpisodeResolver{
		baseURL: baseURL, caller: caller, key: append([]byte(nil), key...), client: secureHTTPClient(3 * time.Second),
		now: time.Now, nonce: randomNonce,
	}, nil
}

func (r *CoordinatorEpisodeResolver) Resolve(ctx context.Context, executionAccountID, intentID string) (OpenEpisode, error) {
	if !validExecutionID(executionAccountID) || !validHash(intentID) {
		return OpenEpisode{}, errors.New("invalid open episode lookup identity")
	}
	path := "/v1/open-episodes/" + executionAccountID + "/" + intentID
	endpoint, err := coordinatorEndpoint(r.baseURL, path)
	if err != nil {
		return OpenEpisode{}, err
	}
	nonce, err := r.nonce()
	if err != nil || !validNonce(nonce) {
		return OpenEpisode{}, errors.New("coordinator episode nonce unavailable")
	}
	timestamp := strconv.FormatInt(r.now().Unix(), 10)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return OpenEpisode{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-RTC-Caller", r.caller)
	request.Header.Set("X-RTC-Timestamp", timestamp)
	request.Header.Set("X-RTC-Nonce", nonce)
	request.Header.Set("X-RTC-Signature", hex.EncodeToString(protocol.RequestMAC(
		r.key, http.MethodGet, path, r.caller, timestamp, nonce, nil,
	)))
	response, err := r.client.Do(request)
	if err != nil {
		return OpenEpisode{}, fmt.Errorf("%w: coordinator request failed", ErrOpenEpisodeUnavailable)
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, maximumCoordinatorResponseBytes)
	if err != nil {
		return OpenEpisode{}, fmt.Errorf("%w: invalid response body", ErrOpenEpisodeUnavailable)
	}
	if err := protocol.VerifyResponseMAC(
		r.key,
		path,
		r.caller,
		nonce,
		response.StatusCode,
		body,
		response.Header.Get("X-RTC-Response-Signature"),
	); err != nil {
		return OpenEpisode{}, fmt.Errorf("%w: invalid response signature", ErrOpenEpisodeUnavailable)
	}
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusServiceUnavailable {
		return OpenEpisode{}, ErrOpenEpisodeUnavailable
	}
	if response.StatusCode != http.StatusOK {
		return OpenEpisode{}, fmt.Errorf("%w: coordinator returned %d", ErrOpenEpisodeUnavailable, response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return OpenEpisode{}, fmt.Errorf("%w: invalid content type", ErrOpenEpisodeUnavailable)
	}
	var episode OpenEpisode
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&episode); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		episode.SchemaVersion != 2 ||
		episode.ExecutionAccountID != executionAccountID || episode.IntentID != intentID ||
		!protocol.IsAllowedUnwindTargetStrategyManifest(episode.TargetStrategyManifestSHA256) {
		return OpenEpisode{}, fmt.Errorf("%w: response identity mismatch", ErrOpenEpisodeUnavailable)
	}
	return episode, nil
}
