package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateConfigDir points os.UserConfigDir at a temp dir on every OS,
// so these tests can never touch the real config.json.
func isolateConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS
	appDir, err := AppDataDir()
	if err != nil {
		t.Fatal(err)
	}
	return appDir
}

func TestSaveLoadRoundTrip(t *testing.T) {
	isolateConfigDir(t)

	cfg := Default()
	cfg.Accounts = []Account{{ID: "a1", DisplayName: "Player", UploadedMatches: map[string]string{"m": "r"}}}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Accounts) != 1 || got.Accounts[0].UploadedMatches["m"] != "r" {
		t.Errorf("round trip lost data: %+v", got)
	}
}

// An unreadable config.json (for example zero length after a power
// loss) must be kept on disk under a new name. The caller continues
// with defaults, and its next Save would otherwise destroy the only
// copy of the account list.
func TestLoadKeepsUnreadableConfig(t *testing.T) {
	appDir := isolateConfigDir(t)
	if err := os.MkdirAll(appDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const damaged = `{"accounts": [{"id": "a1", "uploaded_mat`
	if err := os.WriteFile(filepath.Join(appDir, "config.json"), []byte(damaged), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("expected an error for an unreadable config")
	}

	// What NewApp does next: carry on with defaults and save them.
	if err := Save(Default()); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(appDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.json.corrupt-") {
			kept, _ := os.ReadFile(filepath.Join(appDir, e.Name()))
			if string(kept) != damaged {
				t.Errorf("backup content changed: %q", kept)
			}
			return
		}
	}
	t.Error("the unreadable config.json was not kept; its content is lost after the next Save")
}
