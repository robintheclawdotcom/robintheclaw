package exitquote

import (
	"encoding/json"
	"testing"
)

func TestPayloadBindingRejectsStaleOrMismatchedEpisode(t *testing.T) {
	saga := sagaRecord{PerpFilledBase: 100, PerpUnwoundBase: 40, SpotReceivedRaw: "250000"}
	candidate := candidate("account-payload", "8", PhasePerpAndSpot, 60)
	payload := map[string]json.RawMessage{
		"filled_base":    json.RawMessage(`60`),
		"unwound_before": json.RawMessage(`40`),
	}
	if !payloadMatches(payload, candidate, saga) {
		t.Fatal("exact partial unwind was rejected")
	}
	payload["unwound_before"] = json.RawMessage(`39`)
	if payloadMatches(payload, candidate, saga) {
		t.Fatal("stale unwind cursor was accepted")
	}
	spot := candidate
	spot.Phase, spot.PerpBaseAmount = PhaseSpotOnly, 0
	if !payloadMatches(map[string]json.RawMessage{"spot_amount": json.RawMessage(`"250000"`)}, spot,
		sagaRecord{PerpFilledBase: 100, PerpUnwoundBase: 100, SpotReceivedRaw: "250000"}) {
		t.Fatal("exact spot-only unwind was rejected")
	}
}
