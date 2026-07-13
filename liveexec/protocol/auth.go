package protocol

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

const MaximumClockSkew = 30 * time.Second

type Authenticator struct {
	key    []byte
	caller string
	mu     sync.Mutex
	nonces map[string]int64
	now    func() time.Time
}

func NewAuthenticator(key []byte, caller string) (*Authenticator, error) {
	if len(key) < 32 || caller == "" {
		return nil, errors.New("auth key must be at least 32 bytes and caller is required")
	}
	return &Authenticator{key: append([]byte(nil), key...), caller: caller, nonces: make(map[string]int64), now: time.Now}, nil
}

func (a *Authenticator) Verify(method, path, caller, timestamp, nonce, signature string, body []byte) error {
	if caller != a.caller || nonce == "" || len(nonce) > 128 {
		return errors.New("invalid caller or nonce")
	}
	timestampMS, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return errors.New("invalid auth timestamp")
	}
	now := a.now().UnixMilli()
	if timestampMS < now-MaximumClockSkew.Milliseconds() || timestampMS > now+MaximumClockSkew.Milliseconds() {
		return errors.New("auth timestamp outside allowed skew")
	}
	provided, err := hex.DecodeString(signature)
	if err != nil || len(provided) != sha256.Size {
		return errors.New("invalid auth signature")
	}
	want := RequestMAC(a.key, method, path, caller, timestamp, nonce, body)
	if subtle.ConstantTimeCompare(provided, want) != 1 {
		return errors.New("auth signature mismatch")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for seen, expires := range a.nonces {
		if expires <= now {
			delete(a.nonces, seen)
		}
	}
	key := caller + "\x00" + nonce
	if _, exists := a.nonces[key]; exists {
		return errors.New("auth nonce replay")
	}
	a.nonces[key] = now + (5 * time.Minute).Milliseconds()
	return nil
}

func RequestMAC(key []byte, method, path, caller, timestamp, nonce string, body []byte) []byte {
	bodyHash := sha256.Sum256(body)
	canonical := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s", method, path, caller, timestamp, nonce, hex.EncodeToString(bodyHash[:]))
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	return mac.Sum(nil)
}
