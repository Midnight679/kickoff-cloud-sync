package accounts

import (
	"encoding/json"
	"testing"

	"github.com/Midnight679/kickoff-cloud-sync/internal/config"
)

// TestExportImportSettings_RoundTrip exports from one manager (the
// "old machine") and imports into another (the "new machine") that
// already has a matching account re-added under a different internal
// ID, as it would after a fresh Epic re-authentication. Every global
// setting and the account's preferences should carry over, matched by
// EpicAccountID rather than the (necessarily different) account ID.
func TestExportImportSettings_RoundTrip(t *testing.T) {
	isolateConfigDir(t)

	hour := 7
	oldCfg := config.Default()
	oldCfg.PollIntervalSecs = 1800
	oldCfg.HTTPTimeoutSecs = 45
	oldCfg.DisableGroupPrivateSeries = true // i.e. GroupPrivateSeriesEnabled: false
	oldCfg.FailedUploadsRetryHour = &hour
	oldCfg.FailedUploadsMaxCount = 250
	oldCfg.NotifyOnUploadComplete = true
	oldCfg.Accounts = []config.Account{{
		ID:               "old-internal-id",
		DisplayName:      "SomePlayer",
		EpicAccountID:    "epic-123",
		FriendlyName:     "Main",
		ReplayVisibility: "unlisted",
		Paused:           true,
	}}
	oldMgr := NewManager(oldCfg, nil)

	data, err := oldMgr.ExportSettings()
	if err != nil {
		t.Fatalf("ExportSettings: %v", err)
	}

	newCfg := config.Default()
	newCfg.Accounts = []config.Account{{
		ID:            "new-internal-id", // different ID, same Epic account
		DisplayName:   "SomePlayer",
		EpicAccountID: "epic-123",
	}}
	newMgr := NewManager(newCfg, nil)

	result, err := newMgr.ImportSettings(data)
	if err != nil {
		t.Fatalf("ImportSettings: %v", err)
	}
	if result.AccountsMatched != 1 || result.AccountsSkipped != 0 {
		t.Errorf("got matched=%d skipped=%d, want matched=1 skipped=0", result.AccountsMatched, result.AccountsSkipped)
	}

	if got := newMgr.GetPollIntervalSecs(); got != 1800 {
		t.Errorf("poll interval = %d, want 1800", got)
	}
	if got := newMgr.GetHTTPTimeoutSecs(); got != 45 {
		t.Errorf("http timeout = %d, want 45", got)
	}
	if newMgr.GetGroupPrivateSeriesEnabled() {
		t.Error("group private series should have imported as disabled")
	}
	if got := newMgr.GetFailedUploadsRetryHour(); got != 7 {
		t.Errorf("retry hour = %d, want 7", got)
	}
	if got := newMgr.GetFailedUploadsMaxCount(); got != 250 {
		t.Errorf("max count = %d, want 250", got)
	}
	if !newMgr.GetNotifyOnUploadComplete() {
		t.Error("notify-on-upload should have imported as enabled")
	}

	accts := newMgr.ListAccounts()
	if len(accts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accts))
	}
	if accts[0].FriendlyName != "Main" {
		t.Errorf("friendly name = %q, want %q", accts[0].FriendlyName, "Main")
	}
	if accts[0].ReplayVisibility != "unlisted" {
		t.Errorf("replay visibility = %q, want %q", accts[0].ReplayVisibility, "unlisted")
	}
	if !accts[0].Paused {
		t.Error("account should have imported as paused")
	}
}

// TestImportSettings_SkipsUnmatchedAccounts covers importing onto a
// fresh install with no accounts re-added yet: global settings should
// still apply, and every account preference entry should be counted
// as skipped rather than erroring or panicking.
func TestImportSettings_SkipsUnmatchedAccounts(t *testing.T) {
	isolateConfigDir(t)

	oldCfg := config.Default()
	oldCfg.Accounts = []config.Account{{ID: "a", DisplayName: "X", EpicAccountID: "epic-999"}}
	oldMgr := NewManager(oldCfg, nil)
	data, err := oldMgr.ExportSettings()
	if err != nil {
		t.Fatalf("ExportSettings: %v", err)
	}

	newMgr := NewManager(config.Default(), nil) // no accounts yet
	result, err := newMgr.ImportSettings(data)
	if err != nil {
		t.Fatalf("ImportSettings: %v", err)
	}
	if result.AccountsMatched != 0 || result.AccountsSkipped != 1 {
		t.Errorf("got matched=%d skipped=%d, want matched=0 skipped=1", result.AccountsMatched, result.AccountsSkipped)
	}
}

// TestImportSettings_InvalidJSON checks that a malformed or unrelated
// file produces a clear error instead of a zero-valued silent import.
func TestImportSettings_InvalidJSON(t *testing.T) {
	isolateConfigDir(t)

	m := NewManager(config.Default(), nil)
	if _, err := m.ImportSettings([]byte("not json")); err == nil {
		t.Error("expected an error for invalid JSON, got nil")
	}
}

// TestExportSettings_OmitsAccountsWithoutEpicID guards the export side
// of the EpicAccountID matching scheme: an account added before that
// field existed has nothing to match against on import, so it
// shouldn't appear in the export at all.
func TestExportSettings_OmitsAccountsWithoutEpicID(t *testing.T) {
	isolateConfigDir(t)

	cfg := config.Default()
	cfg.Accounts = []config.Account{{ID: "a", DisplayName: "NoEpicID"}}
	m := NewManager(cfg, nil)

	data, err := m.ExportSettings()
	if err != nil {
		t.Fatalf("ExportSettings: %v", err)
	}
	var export SettingsExport
	if err := json.Unmarshal(data, &export); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	if len(export.Accounts) != 0 {
		t.Errorf("got %d account entries, want 0 for an account with no EpicAccountID", len(export.Accounts))
	}
}
