package accounts

import (
	"testing"

	"github.com/Midnight679/kickoff-cloud-sync/internal/config"
)

func newIdentityTestManager() *Manager {
	cfg := config.Default()
	cfg.Accounts = []config.Account{
		{ID: "a1", DisplayName: "Main", FriendlyName: "ranked", EpicAccountID: "epic-111"},
		{ID: "a2", DisplayName: "Legacy"}, // added before EpicAccountID existed
	}
	return NewManager(cfg, nil)
}

func TestFindByEpicAccountID(t *testing.T) {
	m := newIdentityTestManager()
	m.mu.Lock()
	defer m.mu.Unlock()

	if label, found := m.findByEpicAccountID("epic-111"); !found || label != "Main (ranked)" {
		t.Errorf("known ID: got (%q, %v)", label, found)
	}
	if _, found := m.findByEpicAccountID("epic-999"); found {
		t.Error("unknown ID reported as a duplicate")
	}
	// A login that returned no account ID must never match the legacy
	// account, whose stored ID is also empty.
	if _, found := m.findByEpicAccountID(""); found {
		t.Error("empty ID matched an account")
	}
}

func TestBackfillEpicAccountID(t *testing.T) {
	m := newIdentityTestManager()

	if !m.backfillEpicAccountID("a2", "epic-222") {
		t.Error("legacy account should have been backfilled")
	}
	if got := m.cfg.Accounts[1].EpicAccountID; got != "epic-222" {
		t.Errorf("got %q after backfill", got)
	}
	if m.backfillEpicAccountID("a1", "epic-other") || m.cfg.Accounts[0].EpicAccountID != "epic-111" {
		t.Error("an ID that is already set must never be overwritten")
	}
	if m.backfillEpicAccountID("a2", "") || m.backfillEpicAccountID("missing", "epic-333") {
		t.Error("empty ID or unknown account must be a no-op")
	}
}
