package config

import (
	"encoding/json"
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
	ID                  string `json:"id"`
	DisplayName         string `json:"display_name"`
	FriendlyName        string `json:"friendly_name,omitempty"`
	HasBallchasingToken bool   `json:"has_ballchasing_token"`
	Paused              bool   `json:"paused"`

	// UploadedMatches maps a Rocket League match ID to the
	// ballchasing.com replay ID it was uploaded as. Checked before
	// attempting any upload for this account.
	UploadedMatches map[string]string `json:"uploaded_matches"`

	// LastPollTime is nil until the first successful poll for this
	// account. Persisted so the GUI has something to show right
	// after an app restart, before the next cycle runs.
	LastPollTime *time.Time `json:"last_poll_time,omitempty"`
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

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	appDir := filepath.Join(dir, "kickoff-cloud-sync")
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
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	pendingDir := filepath.Join(dir, "kickoff-cloud-sync", "pending-uploads")
	if err := os.MkdirAll(pendingDir, 0o700); err != nil {
		return "", err
	}
	return pendingDir, nil
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
		return Config{}, err
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
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
