// Package secrets stores per-account tokens (Epic refresh token,
// ballchasing.com API token) in the OS's native credential store —
// Windows Credential Manager, macOS Keychain, or the Linux Secret
// Service — via go-keyring, instead of writing them to config.json
// in plaintext. config.Account only ever holds non-secret metadata
// (a HasBallchasingToken flag, for the UI) — the actual secret
// values live here and nowhere else on disk.
package secrets

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// service is the keyring "service" name every entry is filed under;
// the account ID is the keyring "user" for that service. Together
// they're the lookup key go-keyring uses under the hood.
const service = "rl-replay-uploader"

// ErrUnavailable wraps any keyring error that isn't a simple "not
// found" — e.g. no keyring daemon running on this Linux session.
// Callers should surface this to the user rather than silently
// falling back to plaintext storage.
var ErrUnavailable = errors.New("secure credential storage is unavailable on this system")

type AccountSecrets struct {
	EpicRefreshToken string `json:"epic_refresh_token,omitempty"`
	BallchasingToken string `json:"ballchasing_token,omitempty"`
}

// Load returns the stored secrets for an account, or a zero-value
// AccountSecrets (no error) if nothing has been stored for it yet —
// that's the normal case for a brand-new account before its first
// ConfirmAddAccount call.
func Load(accountID string) (AccountSecrets, error) {
	raw, err := keyring.Get(service, accountID)
	if errors.Is(err, keyring.ErrNotFound) {
		return AccountSecrets{}, nil
	}
	if err != nil {
		return AccountSecrets{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	var s AccountSecrets
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return AccountSecrets{}, fmt.Errorf("stored credential for account was unreadable: %w", err)
	}
	return s, nil
}

// Save overwrites the whole stored secrets blob for an account.
// Callers that want to update just one field (e.g. SetBallchasingToken
// updating the token but not touching the Epic refresh token) must
// Load first, mutate, then Save — there's no partial-update API in
// the underlying keyring.
func Save(accountID string, s AccountSecrets) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := keyring.Set(service, accountID, string(data)); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

// Delete removes an account's stored secrets entirely. Safe to call
// even if nothing was ever stored (not-found is not an error here).
func Delete(accountID string) error {
	err := keyring.Delete(service, accountID)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}
