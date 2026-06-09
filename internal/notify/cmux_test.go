package notify

import "testing"

func TestCMuxAvailable_NoSocket(t *testing.T) {
	// In CI / dev there is normally no cmux socket; the check must return false
	// (and never panic), and is always false off macOS.
	if CMuxAvailable() {
		t.Skip("cmux appears to be running in this environment; skipping no-socket assertion")
	}
}

func TestSendCMux_NoopWhenUnavailable(t *testing.T) {
	if CMuxAvailable() {
		t.Skip("cmux running; skipping no-op assertion")
	}
	// Must not panic or block when cmux is not running.
	SendCMux("Orchestrator", "Test", "should be a silent no-op")
}
