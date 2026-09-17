package accounts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/dank/rlapi"

	"github.com/Midnight679/kickoff-cloud-sync/internal/auth"
	"github.com/Midnight679/kickoff-cloud-sync/internal/config"
	"github.com/Midnight679/kickoff-cloud-sync/internal/httpclient"
	"github.com/Midnight679/kickoff-cloud-sync/internal/matches"
	"github.com/Midnight679/kickoff-cloud-sync/internal/secrets"
	"github.com/Midnight679/kickoff-cloud-sync/internal/uploader"
)

// maxUploadedMatchesCacheBytes caps the size of a single account's
// UploadedMatches dedupe map (JSON-serialized). At roughly 80 bytes
// per entry this is somewhere around 1.3 million matches — nowhere
// near reachable in practice — but it's a hard ceiling rather than
// leaving the cache unbounded. See resetCacheIfOversized below.
const maxUploadedMatchesCacheBytes = 100 * 1024 * 1024 // 100MB

type AuthStatus string

const (
	StatusAuthenticated  AuthStatus = "authenticated"
	StatusNeedsReauth    AuthStatus = "needs_reauth"
	StatusAuthenticating AuthStatus = "authenticating"
)

// Event names emitted via the onEvent callback. The App layer maps
// these onto Wails runtime.EventsEmit calls — this package stays
// framework-agnostic on purpose so it's testable without a GUI.
const (
	EventAccountsChanged = "accounts-changed" // payload: nil — frontend should re-call ListAccounts
	EventMatchDetected   = "match-detected"   // payload: EventPayload
	EventUploadComplete  = "upload-complete"  // payload: EventPayload
	EventUploadError     = "upload-error"     // payload: EventPayload
	EventAuthError       = "auth-error"       // payload: EventPayload
	EventCacheCleared    = "cache-cleared"    // payload: EventPayload — the dedupe cache hit its size cap and was reset
	EventNoMatches       = "no-matches"       // payload: EventPayload — poll succeeded but history had zero entries
	EventReconnected     = "reconnected"      // payload: EventPayload — dropped connection (e.g. DuplicateLogin) silently re-established
	EventNeedsReauth     = "needs-reauth"     // payload: EventPayload — fired once per transition into needs_reauth (not on every retry), for a one-time OS notification
)

type EventPayload struct {
	AccountID string `json:"account_id"`
	MatchID   string `json:"match_id,omitempty"`
	Message   string `json:"message,omitempty"`
	// Manual is true if this event resulted from an explicit "Poll
	// Now" click rather than the shared scheduled cycle — lets the
	// frontend log tell the two apart, since the outcome message
	// itself (e.g. "no matches in history") is otherwise identical.
	Manual bool `json:"manual,omitempty"`
}

// runtimeState is in-memory only — never persisted. Rebuilt on
// startup by attempting each account's saved refresh token.
type runtimeState struct {
	rpc    *rlapi.PsyNetRPC
	status AuthStatus

	// lastPoll is only ever this account's most recently completed
	// poll cycle — overwritten in place every time, never merged or
	// accumulated across cycles. It's deliberately not persisted:
	// this is "what just happened," not history.
	lastPoll *PollResult

	// groupMu, lastPrivateTeamPair, lastPrivateReplayID and
	// lastPrivateGroupID back the private-scrim-series grouping
	// heuristic in assignReplayGroup — see its comment for the actual
	// logic. Like lastPoll, deliberately not persisted: a group
	// streak only matters within a live session. groupMu serializes
	// the finalizeReplay goroutines this account's uploads launch, so
	// two private uploads handled back-to-back (e.g. catching up on a
	// backlog) don't race on this state — those goroutines are
	// launched in match order, though this lock alone doesn't
	// guarantee they acquire it in that same order under heavy
	// scheduler contention, an acceptable tradeoff given how rare
	// multi-match bursts are in practice.
	groupMu             sync.Mutex
	lastPrivateTeamPair [2]string
	lastPrivateReplayID string
	lastPrivateGroupID  string
}

// PollResult summarizes what a single poll cycle did for one
// account. Found is how many matches needed attention this cycle
// (freshly detected, or a queued upload retried); Uploaded is how
// many of those ended in a successful upload; Failed is the rest.
type PollResult struct {
	Found    int `json:"found"`
	Uploaded int `json:"uploaded"`
	Failed   int `json:"failed"`
}

// AccountView is the read-only projection sent to the frontend —
// deliberately excludes tokens.
type AccountView struct {
	ID               string      `json:"id"`
	DisplayName      string      `json:"display_name"`
	FriendlyName     string      `json:"friendly_name,omitempty"`
	AuthStatus       AuthStatus  `json:"auth_status"`
	Paused           bool        `json:"paused"`
	LastPollTime     *time.Time  `json:"last_poll_time,omitempty"`
	NextPollTime     *time.Time  `json:"next_poll_time,omitempty"` // nil if paused or cycle not running
	HasToken         bool        `json:"has_token"`                // whether a ballchasing token is set, without exposing it
	ReplayVisibility string      `json:"replay_visibility"`        // "public", "unlisted", or "private"
	LastPoll         *PollResult `json:"last_poll,omitempty"`      // nil until this account's first poll completes this run
}

// pendingAccount holds an in-progress add-account flow: logged in,
// but not yet confirmed with a ballchasing token — so not persisted
// or added to the account list yet. Lives only in memory; lost on
// app restart if never confirmed, which is fine.
type pendingAccount struct {
	rpc             *rlapi.PsyNetRPC
	refreshToken    string
	epicDisplayName string
	epicAccountID   string
}

// PendingAccountView is returned once the auth-code step of
// SubmitAddAccountCode succeeds, so the frontend can show the
// detected Epic display name while asking for a friendly name +
// ballchasing token before ConfirmAddAccount finalizes things.
type PendingAccountView struct {
	PendingID       string `json:"pending_id"`
	EpicDisplayName string `json:"epic_display_name"`
}

type Manager struct {
	mu       sync.Mutex
	saveMu   sync.Mutex // serializes disk writes so they can never race/interleave — see persist()
	cfg      config.Config
	runtimes map[string]*runtimeState
	pending  map[string]*pendingAccount
	polling  map[string]bool // account IDs currently mid-poll — guards against manual + scheduled overlap
	nextPoll time.Time
	running  bool
	onEvent  func(name string, payload EventPayload)
}

func NewManager(cfg config.Config, onEvent func(name string, payload EventPayload)) *Manager {
	if cfg.HTTPTimeoutSecs > 0 {
		httpclient.SetTimeout(time.Duration(cfg.HTTPTimeoutSecs) * time.Second)
	}
	return &Manager{
		cfg:      cfg,
		runtimes: make(map[string]*runtimeState),
		pending:  make(map[string]*pendingAccount),
		polling:  make(map[string]bool),
		onEvent:  onEvent,
	}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) emit(name string, payload EventPayload) {
	if m.onEvent != nil {
		m.onEvent(name, payload)
	}
}

// persist saves the current config to disk. All writers should call
// this instead of capturing a `cfg := m.cfg` snapshot and calling
// config.Save directly — persist re-reads m.cfg fresh under m.mu at
// the moment it's called (so it always reflects whatever mutation
// most recently landed, from any goroutine) and serializes all
// actual disk writes through saveMu, so two near-simultaneous
// changes (e.g. a manual token edit landing mid-poll-cycle) can
// never race each other out on disk.
//
// The snapshot has to be a deep copy (Clone), not `cfg := m.cfg`: a
// plain struct copy still shares the Accounts backing array and every
// UploadedMatches map with the live config, and config.Save marshals
// it after m.mu is released. A poll recording an upload at that
// moment is a concurrent map iteration + write, which the Go runtime
// treats as fatal (the whole process dies, no recover possible).
func (m *Manager) persist() error {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()

	m.mu.Lock()
	cfg := m.cfg.Clone()
	m.mu.Unlock()

	return config.Save(cfg)
}

// Init attempts a silent login (via saved refresh token) for every
// non-paused persisted account. Call this once at app startup, before
// showing the account list. Accounts whose token has expired end up
// with StatusNeedsReauth rather than popping a browser window —
// reauth is an explicit user action via ReauthAccount.
//
// A paused account gets no Epic API activity at all here — it's
// optimistically marked Authenticated with no live connection yet,
// and ensureConnected verifies/reconnects it lazily the first time
// it's actually polled (manually, or after being resumed), flipping
// it to NeedsReauth then if the stored token turns out to be dead.
// Without this, launching the app while a paused account's owner is
// mid-match could trigger the same DuplicateLogin collision pausing
// is meant to avoid.
func (m *Manager) Init(ctx context.Context) {
	m.mu.Lock()
	accountsSnapshot := append([]config.Account{}, m.cfg.Accounts...)
	m.mu.Unlock()

	for _, acct := range accountsSnapshot {
		if acct.Paused {
			m.mu.Lock()
			m.runtimes[acct.ID] = &runtimeState{rpc: nil, status: StatusAuthenticated}
			m.mu.Unlock()
			continue
		}

		status := StatusNeedsReauth
		var rpc *rlapi.PsyNetRPC

		accountSecrets, err := secrets.Load(acct.ID)
		if err == nil && accountSecrets.EpicRefreshToken != "" {
			result, loginErr := auth.EpicLoginWithRefreshToken(ctx, accountSecrets.EpicRefreshToken)
			if loginErr != nil && isTransientNetErr(loginErr) {
				// Epic never answered (no network yet, DNS failure,
				// timeout), so nothing says the stored token is bad.
				// This is the normal case for a launch-at-login start
				// that beats the network coming up. Use the same lazy
				// state a paused account gets: no live connection, and
				// ensureConnected retries on every poll from here.
				log.Printf("startup login for %s hit a network error, will retry on the next poll: %v", acct.ID, loginErr)
				status = StatusAuthenticated
			}
			if loginErr == nil {
				rpc = result.RPC
				status = StatusAuthenticated

				// Epic rotates the refresh token on every use — persist
				// the new one now, or the next silent login attempt
				// will fail with the old, now-consumed token.
				accountSecrets.EpicRefreshToken = result.RefreshToken
				if saveErr := secrets.Save(acct.ID, accountSecrets); saveErr != nil {
					log.Printf("failed to persist rotated refresh token for %s: %v", acct.ID, saveErr)
				}
				if m.backfillEpicAccountID(acct.ID, result.AccountID) {
					_ = m.persist()
				}
			}
		}
		m.mu.Lock()
		m.runtimes[acct.ID] = &runtimeState{rpc: rpc, status: status}
		m.mu.Unlock()

		if status == StatusNeedsReauth {
			m.emitNeedsReauthNotice(acct.ID)
		}
	}
	m.emit(EventAccountsChanged, EventPayload{})
}

// ListAccounts returns the current view for every configured
// account, for the frontend to render one box per account.
func (m *Manager) ListAccounts() []AccountView {
	m.mu.Lock()
	defer m.mu.Unlock()

	views := make([]AccountView, 0, len(m.cfg.Accounts))
	for _, acct := range m.cfg.Accounts {
		rt := m.runtimes[acct.ID]
		status := StatusNeedsReauth
		if rt != nil {
			status = rt.status
		}

		var nextPoll *time.Time
		if m.running && !acct.Paused {
			np := m.nextPoll
			nextPoll = &np
		}

		var lastPoll *PollResult
		if rt != nil {
			lastPoll = rt.lastPoll
		}

		views = append(views, AccountView{
			ID:               acct.ID,
			DisplayName:      acct.DisplayName,
			FriendlyName:     acct.FriendlyName,
			AuthStatus:       status,
			Paused:           acct.Paused,
			LastPollTime:     acct.LastPollTime,
			NextPollTime:     nextPoll,
			HasToken:         acct.HasBallchasingToken,
			ReplayVisibility: acct.Visibility(),
			LastPoll:         lastPoll,
		})
	}
	return views
}

// NeedsAttention reports whether any account currently needs
// reauthentication — the same condition the frontend uses to turn
// the settings gear red, mirrored here so the tray icon can show it
// too even while the window is hidden.
func (m *Manager) NeedsAttention() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, acct := range m.cfg.Accounts {
		status := StatusNeedsReauth
		if rt := m.runtimes[acct.ID]; rt != nil {
			status = rt.status
		}
		if status == StatusNeedsReauth {
			return true
		}
	}
	return false
}

// SubmitAddAccountCode is step 1 of the add-account flow: it
// exchanges a manually-copied Epic authorization code (from the URL
// returned by auth.GetAuthURL) for a live PsyNet connection and the
// Epic display name. It does NOT persist anything or add the account
// to the list yet — that only happens once ConfirmAddAccount
// validates a ballchasing token for it.
func (m *Manager) SubmitAddAccountCode(ctx context.Context, authCode string) (PendingAccountView, error) {
	result, err := auth.CompleteEpicLogin(ctx, authCode)
	if err != nil {
		return PendingAccountView{}, fmt.Errorf("login failed: %w", err)
	}

	displayName := result.DisplayName
	if displayName == "" {
		displayName = "Unknown Player"
	}

	// Two entries for one Epic account would each hold their own PsyNet
	// session, and PsyNet allows one per account: every poll cycle each
	// entry would find itself kicked (DuplicateLogin), reconnect, and
	// kick the other, and both would upload the same replays.
	m.mu.Lock()
	existingLabel, duplicate := m.findByEpicAccountID(result.AccountID)
	m.mu.Unlock()
	if duplicate {
		_ = result.RPC.Close()
		return PendingAccountView{}, fmt.Errorf("this Epic account is already added as %s", existingLabel)
	}

	id := newID()
	m.mu.Lock()
	m.pending[id] = &pendingAccount{
		rpc:             result.RPC,
		refreshToken:    result.RefreshToken,
		epicDisplayName: displayName,
		epicAccountID:   result.AccountID,
	}
	m.mu.Unlock()

	return PendingAccountView{PendingID: id, EpicDisplayName: displayName}, nil
}

// ConfirmAddAccount is step 2: validates the ballchasing token
// against ballchasing's own API (so a typo'd or revoked token is
// caught immediately, not on the first real upload), then persists
// the account with the given friendly name. On a validation error,
// the pending login is left in place — the frontend can let the
// user retry with a different token without going through the
// browser login again.
func (m *Manager) ConfirmAddAccount(ctx context.Context, pendingID, ballchasingToken, friendlyName string) (AccountView, error) {
	m.mu.Lock()
	pending, ok := m.pending[pendingID]
	m.mu.Unlock()
	if !ok {
		return AccountView{}, errors.New("no pending login found for that ID — start over")
	}

	if err := uploader.ValidateToken(ballchasingToken); err != nil {
		return AccountView{}, fmt.Errorf("ballchasing token invalid: %w", err)
	}

	if err := secrets.Save(pendingID, secrets.AccountSecrets{
		EpicRefreshToken: pending.refreshToken,
		BallchasingToken: ballchasingToken,
	}); err != nil {
		return AccountView{}, fmt.Errorf("saving credentials securely: %w", err)
	}

	acct := config.Account{
		ID:                  pendingID,
		DisplayName:         pending.epicDisplayName,
		EpicAccountID:       pending.epicAccountID,
		FriendlyName:        friendlyName,
		HasBallchasingToken: true,
		UploadedMatches:     make(map[string]string),
	}

	m.mu.Lock()
	m.cfg.Accounts = append(m.cfg.Accounts, acct)
	m.runtimes[pendingID] = &runtimeState{rpc: pending.rpc, status: StatusAuthenticated}
	delete(m.pending, pendingID)
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		return AccountView{}, err
	}

	m.emit(EventAccountsChanged, EventPayload{})
	return m.viewFor(pendingID), nil
}

// CancelAddAccount discards an in-progress add-account flow (e.g.
// the user closed the "enter token" dialog without confirming).
// Nothing was persisted, so this is just cleanup.
func (m *Manager) CancelAddAccount(pendingID string) {
	m.mu.Lock()
	pending := m.pending[pendingID]
	delete(m.pending, pendingID)
	m.mu.Unlock()

	// The login in step 1 opened a live connection for an account that
	// is now never going to be added.
	if pending != nil {
		closeRPC(pending.rpc)
	}
}

// BeginReauth flags an existing account as mid-login, so the UI can
// show a loading state while the user goes through the Epic login
// URL (see auth.GetAuthURL) and copies back an authorization code.
// Call SubmitReauthCode once they have it.
func (m *Manager) BeginReauth(id string) error {
	m.mu.Lock()
	idx := m.indexOf(id)
	if idx == -1 {
		m.mu.Unlock()
		return errors.New("account not found")
	}
	if rt, ok := m.runtimes[id]; ok {
		rt.status = StatusAuthenticating
	}
	m.mu.Unlock()
	m.emit(EventAccountsChanged, EventPayload{})
	return nil
}

// CancelReauth reverts the "authenticating" status set by BeginReauth
// back to "needs_reauth" — call this if the user backs out of the
// reauth dialog before submitting a code. Without this, an abandoned
// reauth would leave the account stuck showing "authenticating"
// (which hides the Reauthenticate button) until the app restarts.
// A no-op if the account isn't currently mid-reauth.
func (m *Manager) CancelReauth(id string) {
	m.mu.Lock()
	if rt, ok := m.runtimes[id]; ok && rt.status == StatusAuthenticating {
		rt.status = StatusNeedsReauth
	}
	m.mu.Unlock()
	m.emit(EventAccountsChanged, EventPayload{})
}

// SubmitReauthCode completes the reauth flow started by BeginReauth,
// exchanging a manually-copied Epic authorization code for a fresh
// PsyNet connection and updating the account's stored refresh token
// + display name.
func (m *Manager) SubmitReauthCode(ctx context.Context, id, authCode string) error {
	result, err := auth.CompleteEpicLogin(ctx, authCode)
	if err != nil {
		m.mu.Lock()
		if rt, ok := m.runtimes[id]; ok {
			rt.status = StatusNeedsReauth
		}
		m.mu.Unlock()
		m.emit(EventAuthError, EventPayload{AccountID: id, Message: err.Error()})
		return err
	}

	m.mu.Lock()
	idx := m.indexOf(id)
	if idx == -1 {
		m.mu.Unlock()
		return errors.New("account was removed during reauth")
	}
	if want := m.cfg.Accounts[idx].EpicAccountID; want != "" && result.AccountID != "" && want != result.AccountID {
		// The browser was signed into a different Epic account. Taking
		// this login would silently turn this entry into that other
		// account: its name, dedupe history and ballchasing token would
		// no longer belong together.
		label := m.accountLabel(id)
		if rt, ok := m.runtimes[id]; ok {
			rt.status = StatusNeedsReauth
		}
		m.mu.Unlock()
		_ = result.RPC.Close()
		err := fmt.Errorf("you logged in as %q, but this entry is for %s: log in with that Epic account, or add %q as its own account", result.DisplayName, label, result.DisplayName)
		m.emit(EventAuthError, EventPayload{AccountID: id, Message: err.Error()})
		m.emit(EventAccountsChanged, EventPayload{})
		return err
	}
	if m.cfg.Accounts[idx].EpicAccountID == "" {
		m.cfg.Accounts[idx].EpicAccountID = result.AccountID
	}
	if result.DisplayName != "" {
		m.cfg.Accounts[idx].DisplayName = result.DisplayName
	}
	var replaced *rlapi.PsyNetRPC
	if old := m.runtimes[id]; old != nil && !m.polling[id] {
		replaced = old.rpc
	}
	m.runtimes[id] = &runtimeState{rpc: result.RPC, status: StatusAuthenticated}
	m.mu.Unlock()
	closeRPC(replaced)

	existing, err := secrets.Load(id)
	if err != nil {
		m.emit(EventAuthError, EventPayload{AccountID: id, Message: err.Error()})
		return err
	}
	existing.EpicRefreshToken = result.RefreshToken
	if err := secrets.Save(id, existing); err != nil {
		m.emit(EventAuthError, EventPayload{AccountID: id, Message: err.Error()})
		return err
	}

	_ = m.persist()
	m.emit(EventAccountsChanged, EventPayload{})
	return nil
}

func (m *Manager) PauseAccount(id string, paused bool) error {
	m.mu.Lock()
	idx := m.indexOf(id)
	if idx == -1 {
		m.mu.Unlock()
		return errors.New("account not found")
	}
	m.cfg.Accounts[idx].Paused = paused
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		return err
	}
	m.emit(EventAccountsChanged, EventPayload{})
	return nil
}

func (m *Manager) RemoveAccount(id string) error {
	m.mu.Lock()
	idx := m.indexOf(id)
	if idx == -1 {
		m.mu.Unlock()
		return errors.New("account not found")
	}
	m.cfg.Accounts = append(m.cfg.Accounts[:idx], m.cfg.Accounts[idx+1:]...)
	// If this account is mid-poll, leave its connection to pollAccount,
	// which closes it once the poll is done. rlapi v0.1.23 panics in the
	// goroutine waiting on a request if the socket is closed under it.
	var removed *rlapi.PsyNetRPC
	if rt := m.runtimes[id]; rt != nil && !m.polling[id] {
		removed = rt.rpc
	}
	delete(m.runtimes, id)
	m.mu.Unlock()
	closeRPC(removed)

	// Best-effort: clean up stored credentials, but don't block
	// removing the account from the list if the keyring is
	// unavailable — an orphaned keyring entry is harmless clutter,
	// whereas failing to remove the account here would leave a
	// broken entry the user can't get rid of from the UI.
	if err := secrets.Delete(id); err != nil {
		log.Printf("could not remove stored credentials for %s: %v", id, err)
	}

	if err := m.persist(); err != nil {
		return err
	}
	m.emit(EventAccountsChanged, EventPayload{})
	return nil
}

func (m *Manager) SetBallchasingToken(id, token string) error {
	m.mu.Lock()
	idx := m.indexOf(id)
	if idx == -1 {
		m.mu.Unlock()
		return errors.New("account not found")
	}
	m.mu.Unlock()

	// Same check ConfirmAddAccount does for a new account. Without it a
	// mistyped token is saved without complaint, shows as "token set",
	// and then every upload fails with a 401 and piles up in the
	// pending-uploads cache until someone reads the log.
	if token != "" {
		if err := uploader.ValidateToken(token); err != nil {
			return fmt.Errorf("ballchasing token invalid: %w", err)
		}
	}

	existing, err := secrets.Load(id)
	if err != nil {
		return err
	}
	existing.BallchasingToken = token
	if err := secrets.Save(id, existing); err != nil {
		return err
	}

	m.mu.Lock()
	idx = m.indexOf(id)
	if idx != -1 {
		m.cfg.Accounts[idx].HasBallchasingToken = token != ""
	}
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		return err
	}
	m.emit(EventAccountsChanged, EventPayload{})
	return nil
}

func (m *Manager) GetPollIntervalSecs() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.PollIntervalSecs
}

func (m *Manager) SetPollIntervalSecs(secs int) error {
	if secs < config.MinPollIntervalSecs {
		return fmt.Errorf("poll interval must be at least %d minutes", config.MinPollIntervalSecs/60)
	}
	m.mu.Lock()
	m.cfg.PollIntervalSecs = secs
	m.mu.Unlock()
	return m.persist()
}

func (m *Manager) GetHTTPTimeoutSecs() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.HTTPTimeoutSecs
}

// SetHTTPTimeoutSecs updates and persists the timeout, and takes
// effect immediately — any request that starts after this call uses
// the new value. In-flight requests keep running against whatever
// timeout they started with.
func (m *Manager) SetHTTPTimeoutSecs(secs int) error {
	if secs < 5 {
		return errors.New("timeout must be at least 5 seconds")
	}
	m.mu.Lock()
	m.cfg.HTTPTimeoutSecs = secs
	m.mu.Unlock()
	httpclient.SetTimeout(time.Duration(secs) * time.Second)
	return m.persist()
}

// GetGroupPrivateSeriesEnabled reports whether the private-scrim-
// series ballchasing grouping heuristic (see assignReplayGroup) is
// currently turned on — the default for every account.
func (m *Manager) GetGroupPrivateSeriesEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.cfg.DisableGroupPrivateSeries
}

// SetGroupPrivateSeriesEnabled turns the grouping heuristic on or
// off, for every account. Turning it off doesn't undo any grouping
// already applied on ballchasing.com — it only stops new group
// assignments going forward.
func (m *Manager) SetGroupPrivateSeriesEnabled(enabled bool) error {
	m.mu.Lock()
	m.cfg.DisableGroupPrivateSeries = !enabled
	m.mu.Unlock()
	return m.persist()
}

// GetSkippedUpdateVersion returns the release tag the user last
// chose "skip this version" on, or "" if none.
func (m *Manager) GetSkippedUpdateVersion() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.SkippedUpdateVersion
}

// SetSkippedUpdateVersion persists version as the one to stop
// automatically notifying about.
func (m *Manager) SetSkippedUpdateVersion(version string) error {
	m.mu.Lock()
	m.cfg.SkippedUpdateVersion = version
	m.mu.Unlock()
	return m.persist()
}

func (m *Manager) SetFriendlyName(id, name string) error {
	m.mu.Lock()
	idx := m.indexOf(id)
	if idx == -1 {
		m.mu.Unlock()
		return errors.New("account not found")
	}
	m.cfg.Accounts[idx].FriendlyName = name
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		return err
	}
	m.emit(EventAccountsChanged, EventPayload{})
	return nil
}

// SetReplayVisibility sets the ballchasing.com visibility used for
// every future upload from this account. Existing already-uploaded
// replays are unaffected — this only applies going forward.
func (m *Manager) SetReplayVisibility(id, visibility string) error {
	switch visibility {
	case "public", "unlisted", "private":
	default:
		return errors.New("visibility must be one of: public, unlisted, private")
	}

	m.mu.Lock()
	idx := m.indexOf(id)
	if idx == -1 {
		m.mu.Unlock()
		return errors.New("account not found")
	}
	m.cfg.Accounts[idx].ReplayVisibility = visibility
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		return err
	}
	m.emit(EventAccountsChanged, EventPayload{})
	return nil
}

// PollAccountNow polls a single account immediately, regardless of
// its paused state — pausing only exempts an account from the
// shared scheduled cycle, not from an explicit manual poll. Returns
// an error if a poll (scheduled or manual) is already in progress
// for this account, rather than letting the two race.
func (m *Manager) PollAccountNow(ctx context.Context, id string) error {
	m.mu.Lock()
	rt, ok := m.runtimes[id]
	m.mu.Unlock()
	if !ok || rt.status != StatusAuthenticated {
		return errors.New("account is not authenticated — reauth first")
	}
	m.retryPendingUploads(ctx, true)
	if !m.pollAccount(ctx, id, rt.rpc, true) {
		return errors.New("a poll is already in progress for this account — try again shortly")
	}
	return nil
}

// StartScheduledPolling runs the single shared poll cycle until ctx
// is cancelled: on each tick, every non-paused authenticated
// account gets polled in turn.
func (m *Manager) StartScheduledPolling(ctx context.Context) {
	m.mu.Lock()
	m.running = true
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			m.running = false
			m.mu.Unlock()
		}()

		for {
			m.runCycle(ctx)

			m.mu.Lock()
			interval := time.Duration(m.cfg.PollIntervalSecs) * time.Second
			m.nextPoll = time.Now().Add(interval)
			m.mu.Unlock()
			m.emit(EventAccountsChanged, EventPayload{})

			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}()
}

func (m *Manager) runCycle(ctx context.Context) {
	m.retryPendingUploads(ctx, false)

	m.mu.Lock()
	type target struct {
		id  string
		rpc *rlapi.PsyNetRPC
	}
	var targets []target
	for _, acct := range m.cfg.Accounts {
		if acct.Paused {
			continue
		}
		if rt := m.runtimes[acct.ID]; rt != nil && rt.status == StatusAuthenticated {
			targets = append(targets, target{id: acct.ID, rpc: rt.rpc})
		}
	}
	m.mu.Unlock()

	for _, t := range targets {
		m.pollAccount(ctx, t.id, t.rpc, false)
	}
}

// ensureConnected returns a live PsyNetRPC connection for the given
// account, silently reconnecting via the stored Epic refresh token if
// the one passed in has dropped. This happens routinely in practice:
// Psyonix closes our connection with "DuplicateLogin" whenever the
// real Rocket League client (or another instance of this app) logs
// into the same account — i.e. every time the user actually plays a
// match, which is exactly when a background uploader needs to keep
// working. Without this, a dropped connection would mean a full
// browser-based reauth after every single gaming session.
//
// Returns ok=false if reconnecting also fails (or there's no stored
// refresh token to try), having already flagged the account
// needs_reauth and emitted an auth-error — the caller can just bail
// out of this poll cycle in that case.
func (m *Manager) ensureConnected(ctx context.Context, id string, rpc *rlapi.PsyNetRPC, manual bool) (*rlapi.PsyNetRPC, bool) {
	if rpc != nil && rpc.IsConnected() {
		return rpc, true
	}

	accountSecrets, err := secrets.Load(id)
	if err != nil || accountSecrets.EpicRefreshToken == "" {
		m.flagNeedsReauth(id, "connection lost and no stored refresh token to reconnect with", manual)
		return nil, false
	}

	result, err := auth.EpicLoginWithRefreshToken(ctx, accountSecrets.EpicRefreshToken)
	if err != nil {
		if isTransientNetErr(err) {
			// The request never got an answer, so the refresh token was
			// not rejected and is still good. Skip this poll and leave
			// the account's status alone; the next cycle tries again.
			// Flagging needs_reauth here would take the account out of
			// the scheduled cycle until a full browser login, over
			// nothing more than a brief network drop.
			m.emit(EventAuthError, EventPayload{AccountID: id, Message: fmt.Sprintf("network error while reconnecting, will retry on the next poll: %v", err), Manual: manual})
			return nil, false
		}
		m.flagNeedsReauth(id, fmt.Sprintf("connection lost and silent reconnect failed: %v", err), manual)
		return nil, false
	}

	// Epic rotates the refresh token on every use — persist the new
	// one now, or the next reconnect attempt will fail with the old,
	// now-consumed token.
	accountSecrets.EpicRefreshToken = result.RefreshToken
	if err := secrets.Save(id, accountSecrets); err != nil {
		log.Printf("failed to persist rotated refresh token for %s: %v", id, err)
	}

	m.mu.Lock()
	if rt, ok := m.runtimes[id]; ok {
		rt.rpc = result.RPC
		rt.status = StatusAuthenticated
	}
	m.mu.Unlock()
	m.backfillEpicAccountID(id, result.AccountID) // saved by pollAccount's persist

	m.emit(EventReconnected, EventPayload{AccountID: id, Message: "connection was dropped (e.g. by DuplicateLogin) and has been silently re-established", Manual: manual})
	return result.RPC, true
}

// isTransientNetErr reports whether err came from the network
// transport itself (no route, DNS failure, timeout, connection reset)
// rather than from Epic or PsyNet answering and rejecting the login.
// rlapi wraps its HTTP and WebSocket errors with %w, so the underlying
// *url.Error / net.Error is still reachable through the chain. An
// actual rejection (expired or revoked refresh token) comes back as a
// plain formatted error and is correctly not matched here.
func isTransientNetErr(err error) bool {
	var urlErr *url.Error
	var netErr net.Error
	return errors.As(err, &urlErr) || errors.As(err, &netErr)
}

// flagNeedsReauth marks an account as needing reauth and emits an
// auth-error with the given message — shared by ensureConnected and
// (indirectly) anything else that discovers the stored connection is
// unusable. Also emits EventNeedsReauth exactly once per transition
// into this state — not on every retry while it stays broken — so
// the App layer can fire a one-time OS notification instead of
// spamming the user every poll cycle for a persistently dead account.
func (m *Manager) flagNeedsReauth(id, message string, manual bool) {
	m.mu.Lock()
	alreadyFlagged := false
	if rt, ok := m.runtimes[id]; ok {
		alreadyFlagged = rt.status == StatusNeedsReauth
		rt.status = StatusNeedsReauth
	}
	m.mu.Unlock()

	m.emit(EventAuthError, EventPayload{AccountID: id, Message: message, Manual: manual})

	if !alreadyFlagged {
		m.emitNeedsReauthNotice(id)
	}
}

// accountLabel returns a display-friendly "DisplayName (FriendlyName)"
// label for an account, or just DisplayName if no friendly name is
// set. Must be called with m.mu already held.
func (m *Manager) accountLabel(id string) string {
	idx := m.indexOf(id)
	if idx == -1 {
		return ""
	}
	label := m.cfg.Accounts[idx].DisplayName
	if m.cfg.Accounts[idx].FriendlyName != "" {
		label = fmt.Sprintf("%s (%s)", label, m.cfg.Accounts[idx].FriendlyName)
	}
	return label
}

// emitNeedsReauthNotice emits EventNeedsReauth for the given account,
// for the App layer to turn into a one-time OS notification. Shared
// by flagNeedsReauth (a live connection died mid-session) and Init
// (an account's stored token was already dead at app startup) — both
// are equally "this needs your attention," and in practice a token
// expiring while the app was closed is the more common of the two.
func (m *Manager) emitNeedsReauthNotice(id string) {
	m.mu.Lock()
	label := m.accountLabel(id)
	m.mu.Unlock()
	if label == "" {
		return
	}
	m.emit(EventNeedsReauth, EventPayload{
		AccountID: id,
		Message:   fmt.Sprintf("%s needs to be reauthenticated — replays aren't being checked for this account.", label),
	})
}

// pollAccount does the actual poll work for one account. It returns
// false without doing anything if this account is already being
// polled (by the scheduled cycle or another manual trigger) —
// callers that care (PollAccountNow) can surface that as an error;
// the scheduled cycle just silently skips it, since it'll be picked
// up on a later tick anyway. manual is true for an explicit "Poll
// Now" click, false for the shared scheduled cycle — threaded through
// to every emitted event so the frontend log can tell them apart.
func (m *Manager) pollAccount(ctx context.Context, id string, rpc *rlapi.PsyNetRPC, manual bool) bool {
	m.mu.Lock()
	if m.polling[id] {
		m.mu.Unlock()
		return false
	}
	m.polling[id] = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.polling, id)
		// The account was removed, or reauthenticated onto a new
		// connection, while this poll was using rpc. Whoever did that
		// left rpc open for this poll to finish with; close it now.
		rt := m.runtimes[id]
		orphaned := rt == nil || rt.rpc != rpc
		m.mu.Unlock()
		if orphaned {
			closeRPC(rpc)
		}
	}()

	rpc, ok := m.ensureConnected(ctx, id, rpc, manual)
	if !ok {
		m.emit(EventAccountsChanged, EventPayload{})
		return true
	}

	// newCount tracks matches handleMatch actually had to do something
	// with (or is still working on via the pending-uploads retry) —
	// distinct from matchCount, which is every entry in history
	// regardless of whether it's already fully accounted for. Without
	// this distinction, an account with a full history of
	// already-uploaded matches would poll successfully every cycle
	// and emit nothing at all, which looks identical to a silent
	// failure.
	newCount := 0
	uploadedCount := 0
	matchCount, err := matches.PollOnce(ctx, rpc, func(matchID, replayURL string) {
		found, uploaded := m.handleMatch(ctx, id, matchID, replayURL, manual)
		if found {
			newCount++
		}
		if uploaded {
			uploadedCount++
		}
	})

	m.mu.Lock()
	idx := m.indexOf(id)
	if idx != -1 {
		now := time.Now()
		m.cfg.Accounts[idx].LastPollTime = &now
	}
	// Overwrites whatever the previous poll left here — this is a
	// snapshot of the cycle that just ran, not a running total.
	if rt := m.runtimes[id]; rt != nil {
		rt.lastPoll = &PollResult{Found: newCount, Uploaded: uploadedCount, Failed: newCount - uploadedCount}
	}
	m.mu.Unlock()
	_ = m.persist()

	if err != nil {
		m.emit(EventAuthError, EventPayload{AccountID: id, Message: err.Error(), Manual: manual})
	} else if newCount == 0 {
		msg := "poll succeeded — no matches in history"
		if matchCount > 0 {
			msg = fmt.Sprintf("poll succeeded — %d match(es) in history, all already uploaded", matchCount)
		}
		m.emit(EventNoMatches, EventPayload{AccountID: id, Message: msg, Manual: manual})
	}
	m.emit(EventAccountsChanged, EventPayload{})
	return true
}

// handleMatch reports two things about one match from this poll:
// found is true if it needed any attention (freshly detected, or
// still waiting on a pending-upload retry) — false if it was already
// fully accounted for. uploaded is true only if this call completed
// a successful upload. pollAccount aggregates both across the whole
// poll into a PollResult.
func (m *Manager) handleMatch(ctx context.Context, accountID, matchID, replayURL string, manual bool) (found, uploaded bool) {
	m.mu.Lock()
	idx := m.indexOf(accountID)
	if idx == -1 {
		m.mu.Unlock()
		return false, false
	}
	_, alreadyUploaded := m.cfg.Accounts[idx].UploadedMatches[matchID]
	hasToken := m.cfg.Accounts[idx].HasBallchasingToken
	visibility := m.cfg.Accounts[idx].Visibility()
	m.mu.Unlock()

	if alreadyUploaded {
		return false, false
	}

	if hasPendingReplay(accountID, matchID) {
		// Already downloaded and cached from an earlier poll, stuck on
		// a retryable failure (e.g. ballchasing's daily upload quota)
		// — retryPendingUploads already retries it once per cycle, so
		// skip re-downloading and re-attempting the upload here too.
		// Still counts as "needs attention," just not by this path.
		return true, false
	}

	m.emit(EventMatchDetected, EventPayload{AccountID: accountID, MatchID: matchID, Manual: manual})

	if replayURL == "" {
		m.emit(EventUploadError, EventPayload{AccountID: accountID, MatchID: matchID, Message: "no replay URL in match history", Manual: manual})
		return true, false
	}
	if !hasToken {
		m.emit(EventUploadError, EventPayload{AccountID: accountID, MatchID: matchID, Message: "no ballchasing token set for this account", Manual: manual})
		return true, false
	}
	accountSecrets, err := secrets.Load(accountID)
	if err != nil || accountSecrets.BallchasingToken == "" {
		m.emit(EventUploadError, EventPayload{AccountID: accountID, MatchID: matchID, Message: "could not read ballchasing token from secure storage", Manual: manual})
		return true, false
	}
	token := accountSecrets.BallchasingToken

	path, err := matches.DownloadReplay(replayURL)
	if err != nil {
		m.emit(EventUploadError, EventPayload{AccountID: accountID, MatchID: matchID, Message: err.Error(), Manual: manual})
		return true, false
	}

	result, err := uploader.UploadReplay(token, path, visibility)
	if err != nil {
		// Download succeeded but the upload itself failed (network
		// hiccup, ballchasing hiccup, etc.) — rather than losing the
		// bytes we already have, cache them for a retry on the next
		// poll cycle instead of leaving them to rot in the OS temp
		// dir (or get cleaned up by the OS before we ever try again).
		if cacheErr := cachePendingReplay(path, accountID, matchID); cacheErr != nil {
			log.Printf("failed to cache replay for retry (%s/%s): %v", accountID, matchID, cacheErr)
		}
		m.emit(EventUploadError, EventPayload{AccountID: accountID, MatchID: matchID, Message: err.Error(), Manual: manual})
		return true, false
	}
	// Uploaded successfully — the temp file has served its purpose.
	_ = os.Remove(path)

	m.mu.Lock()
	idx = m.indexOf(accountID)
	var displayName string
	if idx != -1 {
		displayName = m.cfg.Accounts[idx].DisplayName
		m.cfg.Accounts[idx].UploadedMatches[matchID] = result.ID
		if cleared := m.resetCacheIfOversized(idx, matchID, result.ID); cleared {
			m.mu.Unlock()
			m.emit(EventCacheCleared, EventPayload{
				AccountID: accountID,
				Message:   fmt.Sprintf("dedupe cache exceeded %d bytes and was reset", maxUploadedMatchesCacheBytes),
				Manual:    manual,
			})
			m.mu.Lock()
		}
	}
	m.mu.Unlock()

	_ = m.persist()
	m.emit(EventUploadComplete, EventPayload{AccountID: accountID, MatchID: matchID, Message: result.Location, Manual: manual})
	go m.finalizeReplay(accountID, token, result.ID, displayName)
	return true, true
}

// finalizeReplay does two best-effort things once ballchasing has
// finished parsing a freshly uploaded replay: renames it using
// ballchasing's own authoritative parse (mode, teams, win/loss)
// instead of leaving it as the bare uploaded filename, and applies
// the scrim-series grouping heuristic (see assignReplayGroup).
// Neither is treated as a failure if it doesn't work out — the
// upload itself already succeeded either way. Run in its own
// goroutine since GetReplayWithRetry can take several seconds
// waiting on ballchasing's processing, and there's no need to hold up
// the poll cycle for it.
func (m *Manager) finalizeReplay(accountID, token, replayID, accountDisplayName string) {
	details, err := uploader.GetReplayWithRetry(token, replayID)
	if err != nil {
		log.Printf("could not fetch replay details to finalize %s: %v", replayID, err)
		return
	}

	title := uploader.BuildTitle(details, accountDisplayName)
	if err := uploader.SetReplayTitle(token, replayID, title); err != nil {
		log.Printf("could not set replay title for %s: %v", replayID, err)
	}

	m.assignReplayGroup(accountID, token, replayID, details)
}

// assignReplayGroup implements the "private scrim series" grouping
// heuristic: consecutive private uploads from the same account with
// the same pair of custom team names (see uploader.CustomTeamPair)
// get bundled into one ballchasing group. The group is created
// lazily — only once a second consecutive match confirms the pair
// actually repeats — and the first match of the pair, uploaded
// before that confirmation, is patched into the new group
// retroactively. This means a one-off private match against an
// opponent you never play again never gets a group of its own.
//
// Any non-private upload, or a private one missing a custom name on
// either side, breaks the streak — the next private match (even
// against a previously-seen pair) starts tracking fresh, matching
// "consecutively" as same-account chronological adjacency rather than
// a time window.
func (m *Manager) assignReplayGroup(accountID, token, replayID string, details *uploader.ReplayDetails) {
	m.mu.Lock()
	enabled := !m.cfg.DisableGroupPrivateSeries
	rt := m.runtimes[accountID]
	m.mu.Unlock()
	if !enabled || rt == nil {
		return
	}

	pair, ok := uploader.CustomTeamPair(details)

	rt.groupMu.Lock()
	defer rt.groupMu.Unlock()

	if !ok {
		rt.lastPrivateTeamPair = [2]string{}
		rt.lastPrivateReplayID = ""
		rt.lastPrivateGroupID = ""
		return
	}

	if rt.lastPrivateReplayID == "" || rt.lastPrivateTeamPair != pair {
		// First match of a new (or first-ever) pair — track it, but
		// don't create a group until a second match confirms it.
		rt.lastPrivateTeamPair = pair
		rt.lastPrivateReplayID = replayID
		rt.lastPrivateGroupID = ""
		return
	}

	groupID := rt.lastPrivateGroupID
	if groupID == "" {
		name := fmt.Sprintf("%s vs %s — %s", pair[0], pair[1], time.Now().Format("Jan 2"))
		result, err := uploader.CreateGroup(token, name)
		if err != nil {
			log.Printf("could not create ballchasing group %q: %v", name, err)
			rt.lastPrivateReplayID = replayID
			return
		}
		groupID = result.ID
		if err := uploader.SetReplayGroup(token, rt.lastPrivateReplayID, groupID); err != nil {
			log.Printf("could not add earlier replay %s to group %s: %v", rt.lastPrivateReplayID, groupID, err)
		}
		rt.lastPrivateGroupID = groupID
	}
	if err := uploader.SetReplayGroup(token, replayID, groupID); err != nil {
		log.Printf("could not add replay %s to group %s: %v", replayID, groupID, err)
	}
	rt.lastPrivateReplayID = replayID
}

// closeRPC shuts down a PsyNet connection this app no longer needs.
// rlapi keeps a connection open with its own ping loop until Close is
// called, so dropping the last reference does not end it: the socket,
// its goroutines and the account's live PsyNet session all stay up
// until the app exits. Safe on nil and on an already closed connection.
func closeRPC(rpc *rlapi.PsyNetRPC) {
	if rpc != nil {
		_ = rpc.Close()
	}
}

// indexOf must be called with m.mu already held.
func (m *Manager) indexOf(id string) int {
	for i, a := range m.cfg.Accounts {
		if a.ID == id {
			return i
		}
	}
	return -1
}

// resetCacheIfOversized must be called with m.mu already held. If
// the account's UploadedMatches map (serialized) exceeds
// maxUploadedMatchesCacheBytes, it clears the whole map and
// restarts it fresh with only the just-uploaded match — so the
// match we just handled doesn't immediately look "new" again on the
// next poll. Returns true if a reset happened, so the caller can
// emit EventCacheCleared (outside the lock).
//
// Practically this should never fire — see the size comment on the
// constant — but it's a hard ceiling rather than leaving the cache
// unbounded, per your request.
func (m *Manager) resetCacheIfOversized(idx int, keepMatchID, keepReplayID string) bool {
	data, err := json.Marshal(m.cfg.Accounts[idx].UploadedMatches)
	if err != nil || len(data) <= maxUploadedMatchesCacheBytes {
		return false
	}
	m.cfg.Accounts[idx].UploadedMatches = map[string]string{keepMatchID: keepReplayID}
	return true
}

// findByEpicAccountID returns the display label of the configured
// account with the given Epic account ID, if there is one. Must be
// called with m.mu already held.
func (m *Manager) findByEpicAccountID(epicAccountID string) (label string, found bool) {
	if epicAccountID == "" {
		return "", false
	}
	for _, acct := range m.cfg.Accounts {
		if acct.EpicAccountID == epicAccountID {
			return m.accountLabel(acct.ID), true
		}
	}
	return "", false
}

// backfillEpicAccountID records the Epic account ID on an account that
// was added before the field existed, the first time a login tells us
// what it is. Reports whether anything changed (so the caller knows a
// save is due). Never overwrites an ID that is already set.
func (m *Manager) backfillEpicAccountID(id, epicAccountID string) bool {
	if epicAccountID == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.indexOf(id)
	if idx == -1 || m.cfg.Accounts[idx].EpicAccountID != "" {
		return false
	}
	m.cfg.Accounts[idx].EpicAccountID = epicAccountID
	return true
}

func (m *Manager) viewFor(id string) AccountView {
	for _, v := range m.ListAccounts() {
		if v.ID == id {
			return v
		}
	}
	return AccountView{}
}
