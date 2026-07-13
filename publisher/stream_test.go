package publisher

import "testing"

func TestStreamGapRequiresRESTReconstruction(t *testing.T) {
	var tracker StreamTracker
	if tracker.Observe("session-a", 10) || tracker.Healthy() {
		t.Fatal("first stream event must remain untrusted until reconstruction")
	}
	if !tracker.ConfirmRESTReconstruction("session-a", 10) || !tracker.Healthy() {
		t.Fatal("matching reconstruction should establish stream health")
	}
	if tracker.Observe("session-a", 11) || !tracker.Healthy() {
		t.Fatal("contiguous stream update should preserve health")
	}
	if !tracker.Observe("session-a", 13) || tracker.Healthy() {
		t.Fatal("sequence gap must fail closed")
	}
	if tracker.ConfirmRESTReconstruction("session-a", 12) || tracker.Healthy() {
		t.Fatal("stale reconstruction must not heal a gap")
	}
	if !tracker.ConfirmRESTReconstruction("session-a", 13) || !tracker.Healthy() {
		t.Fatal("current reconstruction should heal the gap")
	}
}
