package accounts

import (
	"errors"
	"testing"

	"github.com/Midnight679/kickoff-cloud-sync/internal/config"
)

// TestMaybeEmitVersionMismatch covers the once-per-run limit that
// only applies to background-triggered detections (startup, silent
// reconnect) — a manual login attempt (add account, reauth) should
// always re-notify, since the user is actively watching it.
func TestMaybeEmitVersionMismatch(t *testing.T) {
	versionErr := errors.New("VersionMismatch: client is out of date")
	unrelatedErr := errors.New("network unreachable")

	var events []string
	m := NewManager(config.Default(), func(name string, _ EventPayload) {
		events = append(events, name)
	})

	m.maybeEmitVersionMismatch(nil, false)
	m.maybeEmitVersionMismatch(unrelatedErr, false)
	if len(events) != 0 {
		t.Fatalf("nil/unrelated errors should never emit, got %v", events)
	}

	m.maybeEmitVersionMismatch(versionErr, false)
	m.maybeEmitVersionMismatch(versionErr, false)
	m.maybeEmitVersionMismatch(versionErr, false)
	if len(events) != 1 || events[0] != EventVersionMismatch {
		t.Errorf("background calls: got %v, want exactly one %q", events, EventVersionMismatch)
	}

	events = nil
	m.maybeEmitVersionMismatch(versionErr, true)
	m.maybeEmitVersionMismatch(versionErr, true)
	if len(events) != 2 {
		t.Errorf("manual calls should re-notify every time: got %d events, want 2", len(events))
	}
}
