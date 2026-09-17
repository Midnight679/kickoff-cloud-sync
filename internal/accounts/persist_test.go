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
