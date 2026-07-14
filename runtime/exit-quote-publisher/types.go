package exitquote

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	"github.com/robin-the-claw/liveexec/protocol"
)

type Phase string

const (
	PhasePerpAndSpot Phase = "perp_and_spot"
	PhaseSpotOnly    Phase = "spot_only"
	maxDatabaseBase        = uint64(1<<63 - 1)
)

var (
	hashPattern    = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	accountPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,63}$`)
	actionPattern  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	decimalPattern = regexp.MustCompile(`^[1-9][0-9]{0,38}$`)
)

type Candidate struct {
	ActionID           string
	ExecutionAccountID string
	IntentID           string
	MarketManifest     string
	SagaVersion        uint64
	SpotAmount         string
	PerpBaseAmount     uint64
	Phase              Phase
}

func (candidate Candidate) validate() error {
	if !actionPattern.MatchString(candidate.ActionID) || !accountPattern.MatchString(candidate.ExecutionAccountID) ||
		!hashPattern.MatchString(candidate.IntentID) || !hashPattern.MatchString(candidate.MarketManifest) ||
		candidate.SagaVersion == 0 || !decimalPattern.MatchString(candidate.SpotAmount) {
		return errors.New("invalid exit candidate identity")
	}
	if candidate.PerpBaseAmount > maxDatabaseBase ||
		(candidate.Phase == PhasePerpAndSpot && candidate.PerpBaseAmount == 0) ||
		(candidate.Phase == PhaseSpotOnly && candidate.PerpBaseAmount != 0) ||
		(candidate.Phase != PhasePerpAndSpot && candidate.Phase != PhaseSpotOnly) {
		return errors.New("invalid exit candidate phase")
	}
	return nil
}

func quoteRequest(candidate Candidate, nowMS uint64) (protocol.QuoteRequest, error) {
	if err := candidate.validate(); err != nil {
		return protocol.QuoteRequest{}, err
	}
	requestedAt := nowMS - nowMS%4_000
	evaluationID := domainHash("robin.exit-quote-evaluation.v1", candidate, 0)
	return protocol.QuoteRequest{
		RequestID:          domainHash("robin.exit-quote-request.v1", candidate, requestedAt),
		ExecutionAccountID: candidate.ExecutionAccountID,
		SourceEvaluationID: evaluationID,
		MarketManifest:     candidate.MarketManifest,
		IntentID:           candidate.IntentID,
		Action:             protocol.ActionUnwind,
		RequestedAtMS:      requestedAt,
	}, nil
}

func domainHash(domain string, candidate Candidate, requestedAt uint64) string {
	material := fmt.Sprintf("%s\x00%s\n%s\n%s\n%d\n%s\n%d\n%s\n%d", domain,
		candidate.ExecutionAccountID, candidate.IntentID, candidate.ActionID, candidate.SagaVersion,
		candidate.SpotAmount, candidate.PerpBaseAmount, candidate.Phase, requestedAt)
	digest := sha256.Sum256([]byte(material))
	return "0x" + hex.EncodeToString(digest[:])
}

type PersistenceEvidence struct {
	SourceSession            string
	SourceEventID            string
	PayloadSHA256            string
	ReceivedAtMS             uint64
	SubmissionDeadlineMS     uint64
	ReconciliationDeadlineMS uint64
	MarkPrice                uint32
	UnwindPhase              Phase
	PerpBaseAmount           uint64
	PerpLimitPrice           uint32
	ExpectedUIMultiplier     string
	MinOracleRoundID         string
}

func evidenceFromQuote(candidate Candidate, request protocol.QuoteRequest, quote protocol.QuoteBundle, nowMS uint64, publicKey ed25519.PublicKey, market uint32) (PersistenceEvidence, error) {
	if err := quote.Verify(publicKey, market, nowMS); err != nil {
		return PersistenceEvidence{}, err
	}
	authority := quote.ExitAuthority
	if quote.RequestID != request.RequestID || quote.ExecutionAccountID != candidate.ExecutionAccountID ||
		quote.SourceEvaluationID != request.SourceEvaluationID || quote.MarketManifest != candidate.MarketManifest ||
		quote.Action != protocol.ActionUnwind || authority == nil || authority.ExecutionAccountID != candidate.ExecutionAccountID ||
		authority.IntentID != candidate.IntentID || authority.MarketManifest != candidate.MarketManifest ||
		quote.Spot.StockAmount != candidate.SpotAmount || quote.Perp.BaseAmount != candidate.PerpBaseAmount ||
		quote.Perp.Phase != string(candidate.Phase) || !digestPattern.MatchString(authority.PayloadSHA256) {
		return PersistenceEvidence{}, errors.New("exit quote identity mismatch")
	}
	return PersistenceEvidence{
		SourceSession: authority.SourceSession, SourceEventID: authority.SourceEventID,
		PayloadSHA256: authority.PayloadSHA256, ReceivedAtMS: authority.ReceivedAtMS,
		SubmissionDeadlineMS:     authority.SubmissionDeadlineMS,
		ReconciliationDeadlineMS: authority.ReconciliationDeadlineMS,
		MarkPrice:                quote.Perp.MarkPrice, UnwindPhase: candidate.Phase,
		PerpBaseAmount: quote.Perp.BaseAmount, PerpLimitPrice: quote.Perp.LimitPrice,
		ExpectedUIMultiplier: quote.Spot.ExpectedUIMultiplier,
		MinOracleRoundID:     quote.Spot.MinOracleRoundID,
	}, nil
}

func validPositiveUint(value, maximum string) bool {
	if value == "" || value[0] == '0' || len(value) > len(maximum) {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return len(value) < len(maximum) || value <= maximum
}
