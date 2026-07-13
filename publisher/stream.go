package publisher

type StreamTracker struct {
	session       string
	sequence      uint64
	gap           bool
	reconstructed bool
}

func (t *StreamTracker) Observe(session string, sequence uint64) bool {
	if session == "" || sequence == 0 {
		t.gap = true
		t.reconstructed = false
		return true
	}
	if t.session != "" && (session != t.session || sequence != t.sequence+1) {
		t.gap = true
		t.reconstructed = false
	}
	t.session = session
	t.sequence = sequence
	return t.gap
}

func (t *StreamTracker) ConfirmRESTReconstruction(session string, sequence uint64) bool {
	if session != t.session || sequence < t.sequence {
		return false
	}
	t.sequence = sequence
	t.gap = false
	t.reconstructed = true
	return true
}

func (t StreamTracker) Healthy() bool {
	return !t.gap && t.reconstructed && t.session != "" && t.sequence > 0
}
