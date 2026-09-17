package accounts

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Midnight679/kickoff-cloud-sync/internal/config"
)

const testRetryHour = 4

func TestDurationUntilNextRetryHour(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		name string
		now  time.Time
		want time.Duration
	}{
		{
			name: "before the target hour today",
			now:  time.Date(2026, 1, 1, 1, 0, 0, 0, loc),
			want: 3 * time.Hour,
		},
		{
			name: "exactly at the target hour",
			now:  time.Date(2026, 1, 1, 4, 0, 0, 0, loc),
			want: 24 * time.Hour, // already passed for today, so it's tomorrow's
		},
		{
			name: "after the target hour today",
			now:  time.Date(2026, 1, 1, 20, 0, 0, 0, loc),
			want: 8 * time.Hour, // 4am tomorrow
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := durationUntilNextRetryHour(testRetryHour, c.now); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// TestDurationUntilNextRetryHour_RespectsConfiguredHour verifies a
// different configured hour actually changes the target, not just the
// default 4am used by the other tests in this file.
func TestDurationUntilNextRetryHour_RespectsConfiguredHour(t *testing.T) {
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	if got, want := durationUntilNextRetryHour(22, now), 12*time.Hour; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNextFailedUploadsRetryWait(t *testing.T) {
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)

	t.Run("never run before", func(t *testing.T) {
		if got := nextFailedUploadsRetryWait(nil, testRetryHour, now); got != 0 {
			t.Errorf("expected an immediate run when never run before, got wait of %v", got)
		}
	})

	t.Run("more than 24h since last run", func(t *testing.T) {
		last := now.Add(-25 * time.Hour)
		if got := nextFailedUploadsRetryWait(&last, testRetryHour, now); got != 0 {
			t.Errorf("expected an immediate catch-up run, got wait of %v", got)
		}
	})

	t.Run("less than 24h since last run", func(t *testing.T) {
		last := now.Add(-1 * time.Hour)
		got := nextFailedUploadsRetryWait(&last, testRetryHour, now)
		if got != durationUntilNextRetryHour(testRetryHour, now) {
			t.Errorf("expected to wait for the next target hour, got %v", got)
		}
		if got <= 0 {
			t.Error("should not run immediately when a retry isn't due yet")
		}
	})
}

func TestHasFailedUpload(t *testing.T) {
	isolateConfigDir(t)

	if hasFailedUpload("acctA", "match1") {
		t.Error("expected false before the file exists")
	}

	dir, err := config.FailedUploadsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "acctA__match1.replay"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !hasFailedUpload("acctA", "match1") {
		t.Error("expected true once the file exists")
	}
	if hasFailedUpload("acctA", "match2") {
		t.Error("expected false for a different match ID")
	}
}

func TestMoveToFailedUploads(t *testing.T) {
	isolateConfigDir(t)
	m := NewManager(config.Default(), nil)

	src := filepath.Join(t.TempDir(), "downloaded.replay")
	if err := os.WriteFile(src, []byte("replay bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := m.moveToFailedUploads(src, "acctA", "match1"); err != nil {
		t.Fatalf("moveToFailedUploads: %v", err)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source file should no longer exist after the move")
	}
	if !hasFailedUpload("acctA", "match1") {
		t.Error("the replay should now be in the failed-uploads directory")
	}
}

// TestMoveToFailedUploads_RespectsConfiguredCap verifies
// SetFailedUploadsMaxCount's value is what's actually enforced, not
// just the hardcoded default.
func TestMoveToFailedUploads_RespectsConfiguredCap(t *testing.T) {
	isolateConfigDir(t)
	cfg := config.Default()
	cfg.FailedUploadsMaxCount = 2
	m := NewManager(cfg, nil)

	dir, err := config.FailedUploadsDir()
	if err != nil {
		t.Fatal(err)
	}

	for i, name := range []string{"acct__match000.replay", "acct__match001.replay"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
		mtime := time.Now().Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	src := filepath.Join(t.TempDir(), "new.replay")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.moveToFailedUploads(src, "acct", "matchNEW"); err != nil {
		t.Fatalf("moveToFailedUploads: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("expected the configured cap of 2 to be enforced, got %d files", len(entries))
	}
	if _, err := os.Stat(filepath.Join(dir, "acct__match000.replay")); !os.IsNotExist(err) {
		t.Error("the oldest file should have been evicted under the configured cap of 2")
	}
}

// TestEvictOldestFailedUploadsIfFull is the core of the cap this
// feature was asked for: once the directory holds maxCount replays,
// adding one more must delete the single oldest one (by modification
// time) to make room, never more and never less.
func TestEvictOldestFailedUploadsIfFull(t *testing.T) {
	isolateConfigDir(t)
	m := NewManager(config.Default(), nil)
	maxCount := config.DefaultFailedUploadsMaxCount

	dir, err := config.FailedUploadsDir()
	if err != nil {
		t.Fatal(err)
	}

	// Fill the directory to exactly the cap, with distinct, increasing
	// mtimes so "oldest" is unambiguous.
	base := time.Now().Add(-time.Duration(maxCount) * time.Minute)
	for i := 0; i < maxCount; i++ {
		name := filepath.Join(dir, fmt.Sprintf("acct__match%03d.replay", i))
		if err := os.WriteFile(name, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
		mtime := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(name, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	oldestPath := filepath.Join(dir, "acct__match000.replay") // i=0, earliest mtime

	src := filepath.Join(t.TempDir(), "new.replay")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.moveToFailedUploads(src, "acct", "matchNEW"); err != nil {
		t.Fatalf("moveToFailedUploads: %v", err)
	}

	if _, err := os.Stat(oldestPath); !os.IsNotExist(err) {
		t.Error("the oldest failed upload should have been evicted to make room")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != maxCount {
		t.Errorf("expected exactly %d files after eviction, got %d", maxCount, len(entries))
	}
}

// TestEvictOldestFailedUploadsIfFull_UnderCapDoesNothing guards
// against evicting anything when the directory isn't actually full —
// the cap should never trim a backlog that hasn't reached it yet.
func TestEvictOldestFailedUploadsIfFull_UnderCapDoesNothing(t *testing.T) {
	isolateConfigDir(t)

	dir, err := config.FailedUploadsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "acct__match1.replay"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	evictOldestFailedUploadsIfFull(dir, config.DefaultFailedUploadsMaxCount)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected the single file to survive, got %d entries", len(entries))
	}
}

// TestSetFailedUploadsRetryHour_ValidatesRange guards the setter
// against an hour outside 0-23, which durationUntilNextRetryHour has
// no sensible behavior for.
func TestSetFailedUploadsRetryHour_ValidatesRange(t *testing.T) {
	isolateConfigDir(t)
	m := NewManager(config.Default(), nil)

	for _, hour := range []int{-1, 24, 100} {
		if err := m.SetFailedUploadsRetryHour(hour); err == nil {
			t.Errorf("expected an error for hour %d", hour)
		}
	}
	if err := m.SetFailedUploadsRetryHour(0); err != nil {
		t.Errorf("hour 0 (midnight) should be valid: %v", err)
	}
	if got := m.GetFailedUploadsRetryHour(); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// TestSetFailedUploadsMaxCount_EnforcesMinimum guards the setter
// against a cap that would make evictOldestFailedUploadsIfFull try to
// remove more files than exist.
func TestSetFailedUploadsMaxCount_EnforcesMinimum(t *testing.T) {
	isolateConfigDir(t)
	m := NewManager(config.Default(), nil)

	if err := m.SetFailedUploadsMaxCount(0); err == nil {
		t.Error("expected an error for a cap of 0")
	}
	if err := m.SetFailedUploadsMaxCount(-5); err == nil {
		t.Error("expected an error for a negative cap")
	}
	if err := m.SetFailedUploadsMaxCount(1); err != nil {
		t.Errorf("a cap of 1 should be valid: %v", err)
	}
	if got := m.GetFailedUploadsMaxCount(); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}
