package accounts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/dank/rlapi"

	"github.com/yourusername/rl-replay-uploader/internal/auth"
	"github.com/yourusername/rl-replay-uploader/internal/config"
	"github.com/yourusername/rl-replay-uploader/internal/httpclient"
	"github.com/yourusername/rl-replay-uploader/internal/matches"
	"github.com/yourusername/rl-replay-uploader/internal/secrets"
	"github.com/yourusername/rl-replay-uploader/internal/uploader"
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
	EventMatchDetected   = "match-detected"    // payload: EventPayload
	EventUploadComplete  = "upload-complete"   // payload: EventPayload
	EventUploadError     = "upload-error"      // payload: EventPayload
	EventAuthError       = "auth-error"        // payload: EventPayload
	EventCacheCleared    = "cache-cleared"      // payload: EventPayload — the dedupe cache hit its size cap and was reset
)

type EventPayload struct {
	AccountID string `json:"account_id"`
	MatchID   string `json:"match_id,omitempty"`
	Message   string `json:"message,omitempty"`
}

// runtimeState is in-memory only — never persisted. Rebuilt on
// startup by attempting each account's saved refresh token.
type runtimeState struct {
	rpc    *rlapi.PsyNetRPC
	status AuthStatus
}

// AccountView is the read-only projection sent to the frontend —
// deliberately excludes tokens.
type AccountView struct {
	ID           string     `json:"id"`
	DisplayName  string     `json:"display_name"`
	FriendlyName string     `json:"friendly_name,omitempty"`
	AuthStatus   AuthStatus `json:"auth_status"`
	Paused       bool       `json:"paused"`
	LastPollTime *time.Time `json:"last_poll_time,omitempty"`
	NextPollTime *time.Time `json:"next_poll_time,omitempty"` // nil if paused or cycle not running
	HasToken     bool       `json:"has_token"`                // whether a ballchasing token is set, without exposing it
}

// pendingAccount holds an in-progress add-account flow: logged in,
// but not yet confirmed with a ballchasing token — so not persisted
// or added to the account list yet. Lives only in memory; lost on
// app restart if never confirmed, which is fine.
type pendingAccount struct {
	rpc             *rlapi.PsyNetRPC
	refreshToken    string
	epicDisplayName string
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
func (m *Manager) persist() error {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()

	m.mu.Lock()
	cfg := m.cfg
	m.mu.Unlock()

	return config.Save(cfg)
}

// Init attempts a silent login (via saved refresh token) for every
// persisted account. Call this once at app startup, before showing
// the account list. Accounts whose token has expired end up with
// StatusNeedsReauth rather than popping a browser window — reauth
// is an explicit user action via ReauthAccount.
func (m *Manager) Init(ctx context.Context) {
	m.mu.Lock()
	accountsSnapshot := append([]config.Account{}, m.cfg.Accounts...)
	m.mu.Unlock()

	for _, acct := range accountsSnapshot {
		status := StatusNeedsReauth
		var rpc *rlapi.PsyNetRPC

		accountSecrets, err := secrets.Load(acct.ID)
		if err == nil && accountSecrets.EpicRefreshToken != "" {
			result, loginErr := auth.EpicLoginWithRefreshToken(ctx, accountSecrets.EpicRefreshToken)
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
			}
		}
		m.mu.Lock()
		m.runtimes[acct.ID] = &runtimeState{rpc: rpc, status: status}
		m.mu.Unlock()
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

		views = append(views, AccountView{
			ID:           acct.ID,
			DisplayName:  acct.DisplayName,
			FriendlyName: acct.FriendlyName,
			AuthStatus:   status,
			Paused:       acct.Paused,
			LastPollTime: acct.LastPollTime,
			NextPollTime: nextPoll,
			HasToken:     acct.HasBallchasingToken,
		})
	}
	return views
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

	id := newID()
	m.mu.Lock()
	m.pending[id] = &pendingAccount{
		rpc:             result.RPC,
		refreshToken:    result.RefreshToken,
		epicDisplayName: displayName,
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
	delete(m.pending, pendingID)
	m.mu.Unlock()
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
	if result.DisplayName != "" {
		m.cfg.Accounts[idx].DisplayName = result.DisplayName
	}
	m.runtimes[id] = &runtimeState{rpc: result.RPC, status: StatusAuthenticated}
	m.mu.Unlock()

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
	delete(m.runtimes, id)
	m.mu.Unlock()

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
	m.retryPendingUploads(ctx)
	if !m.pollAccount(ctx, id, rt.rpc) {
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
	m.retryPendingUploads(ctx)

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
		m.pollAccount(ctx, t.id, t.rpc)
	}
}

// pollAccount does the actual poll work for one account. It returns
// false without doing anything if this account is already being
// polled (by the scheduled cycle or another manual trigger) —
// callers that care (PollAccountNow) can surface that as an error;
// the scheduled cycle just silently skips it, since it'll be picked
// up on a later tick anyway.
func (m *Manager) pollAccount(ctx context.Context, id string, rpc *rlapi.PsyNetRPC) bool {
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
		m.mu.Unlock()
	}()

	err := matches.PollOnce(ctx, rpc, func(matchID, replayURL string) {
		m.handleMatch(ctx, id, matchID, replayURL)
	})

	m.mu.Lock()
	idx := m.indexOf(id)
	if idx != -1 {
		now := time.Now()
		m.cfg.Accounts[idx].LastPollTime = &now
	}
	m.mu.Unlock()
	_ = m.persist()

	if err != nil {
		m.emit(EventAuthError, EventPayload{AccountID: id, Message: err.Error()})
	}
	m.emit(EventAccountsChanged, EventPayload{})
	return true
}

func (m *Manager) handleMatch(ctx context.Context, accountID, matchID, replayURL string) {
	m.mu.Lock()
	idx := m.indexOf(accountID)
	if idx == -1 {
		m.mu.Unlock()
		return
	}
	_, alreadyUploaded := m.cfg.Accounts[idx].UploadedMatches[matchID]
	hasToken := m.cfg.Accounts[idx].HasBallchasingToken
	m.mu.Unlock()

	if alreadyUploaded {
		return
	}

	m.emit(EventMatchDetected, EventPayload{AccountID: accountID, MatchID: matchID})

	if replayURL == "" {
		m.emit(EventUploadError, EventPayload{AccountID: accountID, MatchID: matchID, Message: "no replay URL in match history"})
		return
	}
	if !hasToken {
		m.emit(EventUploadError, EventPayload{AccountID: accountID, MatchID: matchID, Message: "no ballchasing token set for this account"})
		return
	}
	accountSecrets, err := secrets.Load(accountID)
	if err != nil || accountSecrets.BallchasingToken == "" {
		m.emit(EventUploadError, EventPayload{AccountID: accountID, MatchID: matchID, Message: "could not read ballchasing token from secure storage"})
		return
	}
	token := accountSecrets.BallchasingToken

	path, err := matches.DownloadReplay(replayURL)
	if err != nil {
		m.emit(EventUploadError, EventPayload{AccountID: accountID, MatchID: matchID, Message: err.Error()})
		return
	}

	result, err := uploader.UploadReplay(token, path, "public")
	if err != nil {
		// Download succeeded but the upload itself failed (network
		// hiccup, ballchasing hiccup, etc.) — rather than losing the
		// bytes we already have, cache them for a retry on the next
		// poll cycle instead of leaving them to rot in the OS temp
		// dir (or get cleaned up by the OS before we ever try again).
		if cacheErr := cachePendingReplay(path, accountID, matchID); cacheErr != nil {
			log.Printf("failed to cache replay for retry (%s/%s): %v", accountID, matchID, cacheErr)
		}
		m.emit(EventUploadError, EventPayload{AccountID: accountID, MatchID: matchID, Message: err.Error()})
		return
	}
	// Uploaded successfully — the temp file has served its purpose.
	_ = os.Remove(path)

	m.mu.Lock()
	idx = m.indexOf(accountID)
	if idx != -1 {
		m.cfg.Accounts[idx].UploadedMatches[matchID] = result.ID
		if cleared := m.resetCacheIfOversized(idx, matchID, result.ID); cleared {
			m.mu.Unlock()
			m.emit(EventCacheCleared, EventPayload{
				AccountID: accountID,
				Message:   fmt.Sprintf("dedupe cache exceeded %d bytes and was reset", maxUploadedMatchesCacheBytes),
			})
			m.mu.Lock()
		}
	}
	m.mu.Unlock()

	_ = m.persist()
	m.emit(EventUploadComplete, EventPayload{AccountID: accountID, MatchID: matchID, Message: result.Location})
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

func (m *Manager) viewFor(id string) AccountView {
	for _, v := range m.ListAccounts() {
		if v.ID == id {
			return v
		}
	}
	return AccountView{}
}
