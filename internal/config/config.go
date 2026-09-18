package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Account is one configured Rocket League account, persisted to
// disk. Each account authenticates and uploads independently.
//
// NOTE: this struct deliberately holds NO secrets. The Epic refresh
// token and ballchasing API token live in the OS credential store
// (see internal/secrets), keyed by this account's ID — never in
// config.json. HasBallchasingToken is just a non-secret flag so the
// UI can show whether a token is set without a keyring round-trip
// on every ListAccounts call.
type Account struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`

	// EpicAccountID is Epic's own stable ID for this account (not a
	// secret). Empty for an account added before this field existed,
	// until its next successful login fills it in. Used to refuse
	// adding the same Epic account twice and to refuse a reauth that
	// logged into a different Epic account than this entry is for.
	EpicAccountID string `json:"epic_account_id,omitempty"`

	FriendlyName        string `json:"friendly_name,omitempty"`
	HasBallchasingToken bool   `json:"has_ballchasing_token"`
	Paused              bool   `json:"paused"`

	// ReplayVisibility is one of "public", "unlisted", "private" —
	// passed straight through to ballchasing's upload API. Empty
	// means an account created before this setting existed; treat as
	// DefaultReplayVisibility rather than migrating old configs.
	ReplayVisibility string `json:"replay_visibility,omitempty"`

	// UploadedMatches maps a Rocket League match ID to the
	// ballchasing.com replay ID it was uploaded as. Checked before
	// attempting any upload for this account.
	UploadedMatches map[string]string `json:"uploaded_matches"`

	// LastPollTime is nil until the first successful poll for this
	// account. Persisted so the GUI has something to show right
	// after an app restart, before the next cycle runs.
	LastPollTime *time.Time `json:"last_poll_time,omitempty"`
}

// DefaultReplayVisibility is used for any account with no explicit
// ReplayVisibility set (including every account created before this
// setting existed).
const DefaultReplayVisibility = "public"

// Visibility returns the account's configured replay visibility,
// falling back to DefaultReplayVisibility if unset.
func (a Account) Visibility() string {
	if a.ReplayVisibility == "" {
		return DefaultReplayVisibility
	}
	return a.ReplayVisibility
}

// Config is the whole app's persisted state. Contains no secrets —
// see the NOTE on Account above.
type Config struct {
	// PollIntervalSecs governs the single shared poll cycle that
	// checks every non-paused account each time it runs.
	PollIntervalSecs int       `json:"poll_interval_secs"`
	Accounts         []Account `json:"accounts"`

	// HTTPTimeoutSecs caps every outbound network call (replay
	// download, ballchasing upload, token validation). Without a
	// timeout, one hung request can stall the whole poll cycle
	// indefinitely, since accounts are polled one at a time.
	HTTPTimeoutSecs int `json:"http_timeout_secs"`

	// SkippedUpdateVersion is the latest release tag the user chose
	// "skip this version" on, if any — automatic update checks
	// (startup, the 24h periodic recheck) suppress the notice for
	// exactly this version. A manual check from Settings always
	// reports the real state regardless of this.
	SkippedUpdateVersion string `json:"skipped_update_version,omitempty"`

	// DisableGroupPrivateSeries turns off the private-scrim-series
	// ballchasing grouping heuristic (see assignReplayGroup in
	// internal/accounts) when true. Stored inverted (rather than e.g.
	// GroupPrivateSeriesEnabled) so the feature defaults to on for
	// every existing config on disk from before this setting existed
	// — Go's zero value for a bool is false either way, so "off by
	// default" would otherwise be indistinguishable from "the user
	// turned it off."
	DisableGroupPrivateSeries bool `json:"disable_group_private_series,omitempty"`

	// LastFailedUploadsRetry is when the once-daily pass over
	// permanently-failed uploads (see FailedUploadsDir and
	// accounts.runFailedUploadsRetryLoop) last actually ran. nil until
	// the first run. Global rather than per-account since one pass
	// covers every account's failed replays together.
	LastFailedUploadsRetry *time.Time `json:"last_failed_uploads_retry,omitempty"`

	// FailedUploadsRetryHour is the local hour (0-23) the once-daily
	// failed-uploads retry pass targets. nil means "use
	// DefaultFailedUploadsRetryHour" — including every config saved
	// before this setting existed. A pointer rather than a plain int
	// defaulted via omitempty, since 0 (midnight) is itself a valid
	// hour someone might deliberately choose, not just an unset
	// sentinel — unlike FailedUploadsMaxCount below, which has no
	// legitimate zero-or-negative value to protect.
	FailedUploadsRetryHour *int `json:"failed_uploads_retry_hour,omitempty"`

	// FailedUploadsMaxCount caps how many permanently-failed replays
	// are kept (see accounts.evictOldestFailedUploadsIfFull). Zero or
	// negative means "use DefaultFailedUploadsMaxCount" — including
	// every config saved before this setting existed; the setter
	// enforces a minimum of 1 so this never actually happens from a
	// deliberate choice.
	FailedUploadsMaxCount int `json:"failed_uploads_max_count,omitempty"`

	// TotalUploadsEver is a monotonically increasing count of every
	// replay this app has ever uploaded, across every account, for the
	// window-title counter (accounts.Manager.TotalUploadedCount).
	// Incremented once per successful upload and never decremented —
	// deliberately not derived by summing live Account.UploadedMatches
	// map sizes, since that map can be truncated by
	// resetCacheIfOversized's 100MB safety net, which would otherwise
	// make an "all-time" counter visibly go backwards. Zero on a config
	// saved before this field existed; accounts.NewManager backfills it
	// once from the sum of existing UploadedMatches in that case (an
	// exact count for any account that hasn't hit that truncation yet,
	// which in practice is every account).
	TotalUploadsEver int `json:"total_uploads_ever,omitempty"`
}

// DefaultFailedUploadsRetryHour is used when FailedUploadsRetryHour is
// unset — late enough that active gaming is rare, but still a time a
// machine left on for a background uploader is realistically still
// running.
const DefaultFailedUploadsRetryHour = 4

// RetryHour returns the configured failed-uploads retry hour (0-23,
// local time), or DefaultFailedUploadsRetryHour if unset.
func (c Config) RetryHour() int {
	if c.FailedUploadsRetryHour == nil {
		return DefaultFailedUploadsRetryHour
	}
	return *c.FailedUploadsRetryHour
}

// DefaultFailedUploadsMaxCount is used when FailedUploadsMaxCount is
// zero or negative.
const DefaultFailedUploadsMaxCount = 100

// MaxFailedUploads returns the configured failed-uploads cap, or
// DefaultFailedUploadsMaxCount if unset.
func (c Config) MaxFailedUploads() int {
	if c.FailedUploadsMaxCount <= 0 {
		return DefaultFailedUploadsMaxCount
	}
	return c.FailedUploadsMaxCount
}

// Clone returns a deep copy of c: the Accounts slice, every account's
// UploadedMatches map, every LastPollTime, LastFailedUploadsRetry, and
// FailedUploadsRetryHour are all duplicated, so the copy shares no
// mutable memory with c. accounts.Manager.persist depends on this: it
// snapshots the live config under its mutex and then marshals the
// snapshot *outside* it, which is only safe if no other goroutine can
// reach the snapshot's maps, slice elements, or pointed-to values.
func (c Config) Clone() Config {
	out := c
	out.Accounts = make([]Account, len(c.Accounts))
	for i, acct := range c.Accounts {
		if acct.UploadedMatches != nil {
			matches := make(map[string]string, len(acct.UploadedMatches))
			for k, v := range acct.UploadedMatches {
				matches[k] = v
			}
			acct.UploadedMatches = matches
		}
		if acct.LastPollTime != nil {
			t := *acct.LastPollTime
			acct.LastPollTime = &t
		}
		out.Accounts[i] = acct
	}
	if c.LastFailedUploadsRetry != nil {
		t := *c.LastFailedUploadsRetry
		out.LastFailedUploadsRetry = &t
	}
	if c.FailedUploadsRetryHour != nil {
		h := *c.FailedUploadsRetryHour
		out.FailedUploadsRetryHour = &h
	}
	return out
}

// MinPollIntervalSecs is the lowest poll interval allowed — below
// this, the app would hammer Epic's and ballchasing's APIs rapidly
// enough to risk both reliability and account-standing problems, for
// no real benefit (ballchasing's free tier caps uploads at 10/day
// regardless of how often you check).
const MinPollIntervalSecs = 600 // 10 minutes

func Default() Config {
	return Config{
		PollIntervalSecs: MinPollIntervalSecs,
		Accounts:         []Account{},
		HTTPTimeoutSecs:  30,
	}
}

// AppDataDir returns this app's own per-user data directory — the
// exact folder config.json and pending-uploads live in, and nothing
// else. Exported so a caller like the --purge flow (see purge.go)
// can locate it without duplicating path construction, with the same
// safety checks applied every time: it can never resolve to anything
// other than a "kickoff-cloud-sync" subfolder of the OS's own
// per-user config directory, so a caller that RemoveAlls this path
// can never reach outside the app's own data by construction.
func AppDataDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", errors.New("could not determine user config directory")
	}
	appDir := filepath.Join(dir, "kickoff-cloud-sync")
	if !filepath.IsAbs(appDir) || filepath.Base(appDir) != "kickoff-cloud-sync" {
		return "", errors.New("resolved app data path failed a safety check")
	}
	return appDir, nil
}

func configPath() (string, error) {
	appDir, err := AppDataDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(appDir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(appDir, "config.json"), nil
}

// PendingUploadsDir returns (creating if needed) the directory where
// a replay is cached if it downloaded successfully but failed to
// upload — so it can be retried on a later poll instead of being
// lost when its temp file would otherwise get cleaned up.
func PendingUploadsDir() (string, error) {
	appDir, err := AppDataDir()
	if err != nil {
		return "", err
	}
	pendingDir := filepath.Join(appDir, "pending-uploads")
	if err := os.MkdirAll(pendingDir, 0o700); err != nil {
		return "", err
	}
	return pendingDir, nil
}

// FailedUploadsDir returns (creating if needed) the directory where a
// replay is kept if ballchasing permanently rejected it (see
// uploader.UploadError.Permanent) — retried at most once a day (see
// accounts.runFailedUploadsRetryLoop), unlike PendingUploadsDir which
// is retried every poll cycle, since a permanent rejection is far
// less likely to resolve itself between one poll and the next.
func FailedUploadsDir() (string, error) {
	appDir, err := AppDataDir()
	if err != nil {
		return "", err
	}
	failedDir := filepath.Join(appDir, "failed-uploads")
	if err := os.MkdirAll(failedDir, 0o700); err != nil {
		return "", err
	}
	return failedDir, nil
}

func Load() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		// The caller falls back to Default() on any Load error, and the
		// next save then overwrites config.json with that empty config:
		// every account and its whole dedupe history would be gone for
		// good. Move the unreadable file aside first so it can still be
		// inspected or repaired by hand.
		backup := fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix())
		if renameErr := os.Rename(path, backup); renameErr != nil {
			return Config{}, fmt.Errorf("config.json is unreadable (%v) and could not be backed up: %w", err, renameErr)
		}
		return Config{}, fmt.Errorf("config.json was unreadable and has been kept as %s: %w", filepath.Base(backup), err)
	}
	if cfg.Accounts == nil {
		cfg.Accounts = []Account{}
	}
	if cfg.HTTPTimeoutSecs <= 0 {
		cfg.HTTPTimeoutSecs = Default().HTTPTimeoutSecs
	}
	if cfg.PollIntervalSecs < MinPollIntervalSecs {
		cfg.PollIntervalSecs = MinPollIntervalSecs
	}
	for i := range cfg.Accounts {
		if cfg.Accounts[i].UploadedMatches == nil {
			cfg.Accounts[i].UploadedMatches = make(map[string]string)
		}
	}
	return cfg, nil
}

// Save writes the whole config atomically: it writes to a temp file
// in the same directory first, then renames it over the real path.
// Rename is atomic on the same filesystem, so a crash or a second
// concurrent write can't ever leave config.json half-written.
func Save(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "config-*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op if the rename below already moved it

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Flush to disk before the rename. Without this, a power loss or
	// hard freeze shortly after Save can leave the renamed config.json
	// empty: the rename is journaled but the data blocks may not have
	// been written yet.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
