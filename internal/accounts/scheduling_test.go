package accounts

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Midnight679/kickoff-cloud-sync/internal/config"
)

// TestWaitForNextCycle_IntervalChangeRestartsWait reproduces the bug
// where SetPollIntervalSecs had no way to interrupt an already-running
// wait: shortening a long interval used to do nothing until the
// original (long) wait finished on its own. intervalChanged should
// wake waitForNextCycle immediately and have it finish on the new,
// shorter interval instead.
func TestWaitForNextCycle_IntervalChangeRestartsWait(t *testing.T) {
	cfg := config.Default()
	cfg.PollIntervalSecs = 3600 // deliberately too long to ever wait out in a test
	m := NewManager(cfg, nil)

	resultCh := make(chan bool, 1)
	go func() {
		resultCh <- m.waitForNextCycle(context.Background())
	}()

	time.Sleep(20 * time.Millisecond) // let the long wait actually start

	m.mu.Lock()
	m.cfg.PollIntervalSecs = 1
	m.mu.Unlock()
	select {
	case m.intervalChanged <- struct{}{}:
	default:
	}

	select {
	case result := <-resultCh:
		if !result {
			t.Error("expected true (time to poll), got false (ctx cancelled)")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waitForNextCycle did not return after the interval was shortened — the original wait was never interrupted")
	}
}

func TestWaitForNextCycle_CtxCancelledReturnsFalse(t *testing.T) {
	cfg := config.Default()
	cfg.PollIntervalSecs = 3600
	m := NewManager(cfg, nil)

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan bool, 1)
	go func() {
		resultCh <- m.waitForNextCycle(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case result := <-resultCh:
		if result {
			t.Error("expected false after ctx cancellation, got true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitForNextCycle did not return after ctx was cancelled")
	}
}

// TestRetryPendingUploads_FiltersByAccountID reproduces the bug where
// PollAccountNow's manual retry pass touched every account's pending
// uploads, not just the one being polled. Here that's observed via the
// existing "clean up an orphaned file for a removed account" behavior:
// a filtered pass for a different account must leave it alone, while
// an unfiltered pass still cleans it up as before.
func TestRetryPendingUploads_FiltersByAccountID(t *testing.T) {
	isolateConfigDir(t)

	cfg := config.Default()
	cfg.Accounts = []config.Account{{ID: "acctA", DisplayName: "A", UploadedMatches: map[string]string{}}}
	m := NewManager(cfg, nil)

	dir, err := config.PendingUploadsDir()
	if err != nil {
		t.Fatal(err)
	}
	// acctB isn't in cfg.Accounts at all, simulating a removed account
	// — the unfiltered pass's existing "clean up an orphaned file"
	// behavior gives an observable signal for whether a pass actually
	// looked at it.
	orphanPath := filepath.Join(dir, "acctB__match1.replay")
	if err := os.WriteFile(orphanPath, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	m.retryPendingUploads(context.Background(), false, "acctA")
	if _, err := os.Stat(orphanPath); err != nil {
		t.Error("a retry pass filtered to acctA touched acctB's pending file")
	}

	m.retryPendingUploads(context.Background(), false, "")
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Error("an unfiltered retry pass should have cleaned up acctB's orphaned file")
	}
}
