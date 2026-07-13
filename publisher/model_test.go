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
