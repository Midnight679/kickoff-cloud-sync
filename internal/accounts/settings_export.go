package accounts

import (
	"encoding/json"
	"fmt"
)

// SettingsExport is the portable subset of Config written by
// ExportSettings and read back by ImportSettings: every global
// setting, plus each account's non-secret preferences matched back up
// by Epic account ID. Deliberately never includes tokens or refresh
// tokens — those live only in the OS keyring, and an account needs a
// fresh Epic sign-in on another machine regardless of anything in this
// file, so there's nothing meaningful to restore for them here.
type SettingsExport struct {
	PollIntervalSecs          int  `json:"poll_interval_secs"`
	HTTPTimeoutSecs           int  `json:"http_timeout_secs"`
	GroupPrivateSeriesEnabled bool `json:"group_private_series_enabled"`
	FailedUploadsRetryHour    int  `json:"failed_uploads_retry_hour"`
	FailedUploadsMaxCount     int  `json:"failed_uploads_max_count"`
	NotifyOnUploadComplete    bool `json:"notify_on_upload_complete"`

	Accounts []AccountPreferenceExport `json:"accounts"`
}

// AccountPreferenceExport carries one account's non-secret
// preferences, matched back up on import by EpicAccountID — the only
// identifier that survives being removed and re-added on another
// machine (this app's own account IDs are generated fresh each time
// an account is added). DisplayName is included only so the exported
// file is readable by a human; it's never used for matching.
type AccountPreferenceExport struct {
	EpicAccountID    string `json:"epic_account_id"`
	DisplayName      string `json:"display_name"`
	FriendlyName     string `json:"friendly_name,omitempty"`
	ReplayVisibility string `json:"replay_visibility,omitempty"`
	Paused           bool   `json:"paused"`
}

// ExportSettings snapshots every global setting and each account's
// non-secret preferences (see SettingsExport) as indented JSON.
// Accounts with no EpicAccountID (added before that field existed, or
// never completed a successful login) are omitted — there would be
// nothing to match them back up to on import anyway.
func (m *Manager) ExportSettings() ([]byte, error) {
	m.mu.Lock()
	export := SettingsExport{
		PollIntervalSecs:          m.cfg.PollIntervalSecs,
		HTTPTimeoutSecs:           m.cfg.HTTPTimeoutSecs,
		GroupPrivateSeriesEnabled: !m.cfg.DisableGroupPrivateSeries,
		FailedUploadsRetryHour:    m.cfg.RetryHour(),
		FailedUploadsMaxCount:     m.cfg.MaxFailedUploads(),
		NotifyOnUploadComplete:    m.cfg.NotifyOnUploadComplete,
	}
	for _, acct := range m.cfg.Accounts {
		if acct.EpicAccountID == "" {
			continue
		}
		export.Accounts = append(export.Accounts, AccountPreferenceExport{
			EpicAccountID:    acct.EpicAccountID,
			DisplayName:      acct.DisplayName,
			FriendlyName:     acct.FriendlyName,
			ReplayVisibility: acct.ReplayVisibility,
			Paused:           acct.Paused,
		})
	}
	m.mu.Unlock()
	return json.MarshalIndent(export, "", "  ")
}

// ImportSettingsResult summarizes what ImportSettings actually did,
// for the frontend to report back to the user.
type ImportSettingsResult struct {
	AccountsMatched int `json:"accounts_matched"`
	// AccountsSkipped counts entries whose EpicAccountID doesn't match
	// any account currently configured on this machine — normal right
	// after a fresh install, before those accounts are re-added.
	AccountsSkipped int `json:"accounts_skipped"`
}

// ImportSettings applies every global setting from data through the
// same setters Settings' UI uses (so the same validation and
// side-effects — e.g. waking the poll loop on a changed interval —
// apply here too), then applies each account preference entry to
// whichever current account shares its EpicAccountID, if any.
func (m *Manager) ImportSettings(data []byte) (ImportSettingsResult, error) {
	var export SettingsExport
	if err := json.Unmarshal(data, &export); err != nil {
		return ImportSettingsResult{}, fmt.Errorf("not a valid settings export file: %w", err)
	}

	if err := m.SetPollIntervalSecs(export.PollIntervalSecs); err != nil {
		return ImportSettingsResult{}, fmt.Errorf("poll interval: %w", err)
	}
	if err := m.SetHTTPTimeoutSecs(export.HTTPTimeoutSecs); err != nil {
		return ImportSettingsResult{}, fmt.Errorf("network timeout: %w", err)
	}
	if err := m.SetGroupPrivateSeriesEnabled(export.GroupPrivateSeriesEnabled); err != nil {
		return ImportSettingsResult{}, fmt.Errorf("group private series: %w", err)
	}
	if err := m.SetFailedUploadsRetryHour(export.FailedUploadsRetryHour); err != nil {
		return ImportSettingsResult{}, fmt.Errorf("failed-uploads retry hour: %w", err)
	}
	if err := m.SetFailedUploadsMaxCount(export.FailedUploadsMaxCount); err != nil {
		return ImportSettingsResult{}, fmt.Errorf("failed-uploads cap: %w", err)
	}
	if err := m.SetNotifyOnUploadComplete(export.NotifyOnUploadComplete); err != nil {
		return ImportSettingsResult{}, fmt.Errorf("upload notifications: %w", err)
	}

	var result ImportSettingsResult
	for _, pref := range export.Accounts {
		if pref.EpicAccountID == "" {
			continue
		}
		m.mu.Lock()
		var matchID string
		for _, acct := range m.cfg.Accounts {
			if acct.EpicAccountID == pref.EpicAccountID {
				matchID = acct.ID
				break
			}
		}
		m.mu.Unlock()
		if matchID == "" {
			result.AccountsSkipped++
			continue
		}
		if pref.FriendlyName != "" {
			_ = m.SetFriendlyName(matchID, pref.FriendlyName)
		}
		if pref.ReplayVisibility != "" {
			_ = m.SetReplayVisibility(matchID, pref.ReplayVisibility)
		}
		_ = m.PauseAccount(matchID, pref.Paused)
		result.AccountsMatched++
	}
	return result, nil
}
