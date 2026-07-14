package publisher

import (
	"testing"
	"time"
)

func TestStaleEvidenceAndExactMarginMath(t *testing.T) {
	ratio, err := marginRatioMicros("50.000001", "25")
	if err != nil || ratio != 2_000_000 {
		t.Fatalf("unexpected ratio %d: %v", ratio, err)
	}
	if fresh(time.Now().Add(-6*time.Second), time.Now()) {
		t.Fatal("stale evidence must fail")
	}
	if fresh(time.Now().Add(time.Second), time.Now()) {
		t.Fatal("future evidence must fail")
	}
}

func TestDecimalMicrosRequiresExactRepresentation(t *testing.T) {
	value, err := decimalMicros("50.000001")
	if err != nil || value != 50_000_001 {
		t.Fatalf("micros = %d: %v", value, err)
	}
	for _, invalid := range []string{"0.0000001", "1e3", "-1"} {
		if _, err := decimalMicros(invalid); err == nil {
			t.Fatalf("inexact decimal %q was accepted", invalid)
		}
	}
}
