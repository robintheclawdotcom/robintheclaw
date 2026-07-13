package restrictctl

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const signatureDomain = "robin.operator-restriction.v1\x00"

var (
	requestIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,63}$`)
	operatorIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)
	strategyPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{2,63}$`)
	sha256Pattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Scope string

const (
	ScopeGlobal   Scope = "global"
	ScopeStrategy Scope = "strategy"
	ScopeAccount  Scope = "account"
)

type Mode string

const (
	ModeActive     Mode = "ACTIVE"
	ModeReduceOnly Mode = "REDUCE_ONLY"
	ModeHalted     Mode = "HALTED"
)

type Request struct {
	RequestID          string
	Scope              Scope
	StrategyVersion    string
	ExecutionAccountID string
	ExpectedVersion    int64
	FromMode           Mode
	TargetMode         Mode
	Reason             string
	EvidenceSHA256     string
	OperatorID         string
}

type payload struct {
	SchemaVersion      int    `json:"schemaVersion"`
	RequestID          string `json:"requestId"`
	Scope              Scope  `json:"scope"`
	StrategyVersion    string `json:"strategyVersion"`
	ExecutionAccountID string `json:"executionAccountId"`
	ExpectedVersion    int64  `json:"expectedVersion"`
	FromMode           Mode   `json:"fromMode"`
	TargetMode         Mode   `json:"targetMode"`
	ResultingVersion   int64  `json:"resultingVersion"`
	Reason             string `json:"reason"`
	EvidenceSHA256     string `json:"evidenceSha256"`
	OperatorID         string `json:"operatorId"`
	SignerKeyID        string `json:"signerKeyId"`
}

type SignedRequest struct {
	Request       Request
	Canonical     []byte
	SHA256        string
	SignerKeyID   string
	PublicKey     ed25519.PublicKey
	Signature     []byte
	parsedPayload payload
}

func Sign(request Request, privateKey ed25519.PrivateKey, trustedPublicKey ed25519.PublicKey) (SignedRequest, error) {
	if err := Validate(request); err != nil {
		return SignedRequest{}, err
	}
	if len(privateKey) != ed25519.PrivateKeySize || len(trustedPublicKey) != ed25519.PublicKeySize {
		return SignedRequest{}, errors.New("an Ed25519 signing key pair is required")
	}
	derived, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok || !bytes.Equal(derived, trustedPublicKey) {
		return SignedRequest{}, errors.New("signing key does not match the trusted public key")
	}
	keyHash := sha256.Sum256(trustedPublicKey)
	keyID := hex.EncodeToString(keyHash[:])
	p := payload{
		SchemaVersion:      1,
		RequestID:          request.RequestID,
		Scope:              request.Scope,
		StrategyVersion:    request.StrategyVersion,
		ExecutionAccountID: request.ExecutionAccountID,
		ExpectedVersion:    request.ExpectedVersion,
		FromMode:           request.FromMode,
		TargetMode:         request.TargetMode,
		ResultingVersion:   request.ExpectedVersion + 1,
		Reason:             request.Reason,
		EvidenceSHA256:     request.EvidenceSHA256,
		OperatorID:         request.OperatorID,
		SignerKeyID:        keyID,
	}
	canonical, err := json.Marshal(p)
	if err != nil {
		return SignedRequest{}, fmt.Errorf("encode restriction request: %w", err)
	}
	digest := requestDigest(canonical)
	signature := ed25519.Sign(privateKey, digest[:])
	return SignedRequest{
		Request:       request,
		Canonical:     canonical,
		SHA256:        hex.EncodeToString(digest[:]),
		SignerKeyID:   keyID,
		PublicKey:     append(ed25519.PublicKey(nil), trustedPublicKey...),
		Signature:     signature,
		parsedPayload: p,
	}, nil
}

func Validate(request Request) error {
	if !requestIDPattern.MatchString(request.RequestID) {
		return errors.New("request ID must be 8-64 lowercase letters, digits, or hyphens")
	}
	if !operatorIDPattern.MatchString(request.OperatorID) {
		return errors.New("operator ID must be 3-64 lowercase letters, digits, or hyphens")
	}
	if request.ExpectedVersion < 0 {
		return errors.New("expected version must be non-negative")
	}
	if !AllowedTransition(request.FromMode, request.TargetMode) {
		return errors.New("control transition must be ACTIVE to REDUCE_ONLY or HALTED, or REDUCE_ONLY to HALTED")
	}
	if !sha256Pattern.MatchString(request.EvidenceSHA256) {
		return errors.New("evidence digest must be 64 lowercase hexadecimal characters")
	}
	if err := validateReason(request.Reason); err != nil {
		return err
	}
	switch request.Scope {
	case ScopeGlobal:
		if request.StrategyVersion != "" || request.ExecutionAccountID != "" {
			return errors.New("global restrictions cannot include strategy or account identity")
		}
	case ScopeStrategy:
		if !strategyPattern.MatchString(request.StrategyVersion) || request.ExecutionAccountID != "" {
			return errors.New("strategy restrictions require only a valid strategy version")
		}
	case ScopeAccount:
		if !strategyPattern.MatchString(request.StrategyVersion) || !requestIDPattern.MatchString(request.ExecutionAccountID) {
			return errors.New("account restrictions require valid strategy and execution account identities")
		}
	default:
		return errors.New("scope must be global, strategy, or account")
	}
	return nil
}

func AllowedTransition(from, to Mode) bool {
	return (from == ModeActive && (to == ModeReduceOnly || to == ModeHalted)) ||
		(from == ModeReduceOnly && to == ModeHalted)
}

func (signed SignedRequest) Verify() error {
	if err := Validate(signed.Request); err != nil {
		return err
	}
	if len(signed.PublicKey) != ed25519.PublicKeySize || len(signed.Signature) != ed25519.SignatureSize {
		return errors.New("invalid restriction signature material")
	}
	expectedPayload := signed.parsedPayload
	if expectedPayload.SchemaVersion != 1 || expectedPayload.RequestID != signed.Request.RequestID ||
		expectedPayload.Scope != signed.Request.Scope || expectedPayload.StrategyVersion != signed.Request.StrategyVersion ||
		expectedPayload.ExecutionAccountID != signed.Request.ExecutionAccountID ||
		expectedPayload.ExpectedVersion != signed.Request.ExpectedVersion || expectedPayload.FromMode != signed.Request.FromMode ||
		expectedPayload.TargetMode != signed.Request.TargetMode || expectedPayload.ResultingVersion != signed.Request.ExpectedVersion+1 ||
		expectedPayload.Reason != signed.Request.Reason || expectedPayload.EvidenceSHA256 != signed.Request.EvidenceSHA256 ||
		expectedPayload.OperatorID != signed.Request.OperatorID || expectedPayload.SignerKeyID != signed.SignerKeyID {
		return errors.New("restriction request does not match its signed payload")
	}
	canonical, err := json.Marshal(expectedPayload)
	if err != nil || !bytes.Equal(canonical, signed.Canonical) {
		return errors.New("restriction canonical payload mismatch")
	}
	digest := requestDigest(signed.Canonical)
	if hex.EncodeToString(digest[:]) != signed.SHA256 || !ed25519.Verify(signed.PublicKey, digest[:], signed.Signature) {
		return errors.New("restriction signature verification failed")
	}
	keyHash := sha256.Sum256(signed.PublicKey)
	if hex.EncodeToString(keyHash[:]) != signed.SignerKeyID {
		return errors.New("restriction signer identity mismatch")
	}
	return nil
}

func requestDigest(canonical []byte) [sha256.Size]byte {
	value := make([]byte, 0, len(signatureDomain)+len(canonical))
	value = append(value, signatureDomain...)
	value = append(value, canonical...)
	return sha256.Sum256(value)
}

func validateReason(reason string) error {
	if len(reason) < 8 || len(reason) > 512 || strings.TrimSpace(reason) != reason {
		return errors.New("reason must be 8-512 characters without leading or trailing whitespace")
	}
	for _, character := range reason {
		if character < 0x20 || character > 0x7e {
			return errors.New("reason must contain printable ASCII characters only")
		}
	}
	return nil
}
