package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func authorizeRequest(ctx context.Context, store bindingStore, request *http.Request, body, key []byte, caller string, now time.Time) (bool, error) {
	if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-RTC-Caller")), []byte(caller)) != 1 {
		return false, nil
	}
	timestampText := request.Header.Get("X-RTC-Timestamp")
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return false, nil
	}
	signedAt := time.Unix(timestamp, 0)
	if signedAt.Before(now.Add(-30*time.Second)) || signedAt.After(now.Add(30*time.Second)) {
		return false, nil
	}
	nonce := request.Header.Get("X-RTC-Nonce")
	if !validNonce(nonce) {
		return false, nil
	}
	provided, err := hex.DecodeString(request.Header.Get("X-RTC-Signature"))
	if err != nil || len(provided) != sha256.Size {
		return false, nil
	}
	digest := sha256.Sum256(body)
	canonical := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%x", request.Method, request.URL.Path, caller, timestampText, nonce, digest)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	if subtle.ConstantTimeCompare(mac.Sum(nil), provided) != 1 {
		return false, nil
	}
	return store.ClaimNonce(ctx, caller, nonce, signedAt.Add(time.Minute))
}

func validNonce(value string) bool {
	if len(value) < 32 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}
