package accounts

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Midnight679/kickoff-cloud-sync/internal/config"
)

// isolateConfigDir points os.UserConfigDir at a temp dir on every OS,
// so persist() in these tests can never touch the real config.json.
func isolateConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS
}

// TestPersistConcurrentWithMapWrites reproduces the poll goroutine
// recording an upload (a write to Account.UploadedMatches under m.mu)
// while another goroutine (any bound UI method) calls persist(). Before
// the fix, persist() marshalled a shallow copy of m.cfg outside m.mu,
// so encoding/json iterated the same live map the poller was writing
// to. The Go runtime kills the whole process for that ("fatal error:
// concurrent map iteration and map write"); it is not a recoverable panic.
func TestPersistConcurrentWithMapWrites(t *testing.T) {
	isolateConfigDir(t)

	uploaded := make(map[string]string)
	for i := 0; i < 2000; i++ {
		uploaded[fmt.Sprintf("seed%d", i)] = "replay"
	}
	cfg := config.Default()
	cfg.Accounts = []config.Account{{ID: "acct1", DisplayName: "Test", UploadedMatches: uploaded}}
	m := NewManager(cfg, nil)

	deadline := time.Now().Add(2 * time.Second)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() { // stands in for handleMatch recording a finished upload
		defer wg.Done()
		for i := 0; time.Now().Before(deadline); i++ {
			m.mu.Lock()
			m.cfg.Accounts[0].UploadedMatches[fmt.Sprintf("match%d", i%500)] = "replay"
			m.mu.Unlock()
		}
	}()

	wg.Add(1)
	go func() { // stands in for a UI-triggered save (pause, rename, ...)
		defer wg.Done()
		for time.Now().Before(deadline) {
			if err := m.persist(); err != nil {
				t.Errorf("persist: %v", err)
				return
			}
		}
	}()

	wg.Wait()
}

func TestTotalUploadedCount(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = []config.Account{
		{ID: "a", UploadedMatches: map[string]string{"m1": "r1", "m2": "r2"}},
		{ID: "b", UploadedMatches: map[string]string{"m3": "r3"}},
		{ID: "c"}, // nil map — an account created before UploadedMatches existed
	}
	m := NewManager(cfg, nil)

	// TotalUploadsEver was unset (zero) on cfg, so NewManager should
	// have backfilled it from the sum of existing UploadedMatches —
	// this is what keeps the counter correct for anyone upgrading from
	// a version before it existed.
	if got := m.TotalUploadedCount(); got != 3 {
		t.Errorf("got %d, want 3", got)
	}
}

// TestTotalUploadedCount_UnaffectedByMapTruncation reproduces the bug
// where the counter was derived live from summing Account.UploadedMatches
// map sizes, which resetCacheIfOversized's 100MB safety net can
// truncate down to a single entry — visibly decreasing an "all-time"
// counter. It's now backed by config.Config.TotalUploadsEver, a
// monotonic counter that doesn't care what resetCacheIfOversized does
// to the dedupe map afterward.
func TestTotalUploadedCount_UnaffectedByMapTruncation(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = []config.Account{{ID: "a", UploadedMatches: map[string]string{"m1": "r1", "m2": "r2", "m3": "r3"}}}
	m := NewManager(cfg, nil)

	before := m.TotalUploadedCount()
	if before != 3 {
		t.Fatalf("got %d, want 3 before truncation", before)
	}

	// Simulate what resetCacheIfOversized does to an oversized map
	// (truncate to one entry) without needing to actually grow a map
	// past the real 100MB threshold.
	m.mu.Lock()
	m.cfg.Accounts[0].UploadedMatches = map[string]string{"m3": "r3"}
	m.mu.Unlock()

	if got := m.TotalUploadedCount(); got != before {
		t.Errorf("TotalUploadedCount dropped from %d to %d after the dedupe map was truncated", before, got)
	}
}

// TestNewManager_DoesNotDoubleCountBackfill guards the backfill's
// zero-check: a config that already has a real TotalUploadsEver value
// (i.e. wasn't just upgraded) must not have it re-summed from
// UploadedMatches on every restart.
func TestNewManager_DoesNotDoubleCountBackfill(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = []config.Account{{ID: "a", UploadedMatches: map[string]string{"m1": "r1"}}}
	cfg.TotalUploadsEver = 500 // already tracking correctly; far more than UploadedMatches would sum to
	m := NewManager(cfg, nil)

	if got := m.TotalUploadedCount(); got != 500 {
		t.Errorf("got %d, want the existing 500 preserved, not re-derived from UploadedMatches", got)
	}
}
