package main

import (
	"context"
	"log"
	"sync"
	"time"

	"git.sr.ht/~jackmordaunt/go-toast/v2"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Midnight679/kickoff-cloud-sync/internal/accounts"
	"github.com/Midnight679/kickoff-cloud-sync/internal/auth"
	"github.com/Midnight679/kickoff-cloud-sync/internal/autostart"
	"github.com/Midnight679/kickoff-cloud-sync/internal/config"
	"github.com/Midnight679/kickoff-cloud-sync/internal/logbuf"
	"github.com/Midnight679/kickoff-cloud-sync/internal/updatecheck"
)

// appVersion is this build's version, checked against GitHub's
// latest release tag at startup and every updateCheckInterval after
// that (see updateCheckLoop). Bump this alongside wails.json's
// productVersion and the git tag for every release — nothing reads
// this from git automatically.
const appVersion = "0.2.4"

type App struct {
	ctx    context.Context
	mgr    *accounts.Manager
	cancel context.CancelFunc

	updateMu   sync.Mutex
	updateInfo updatecheck.Info
}

func NewApp() *App {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("failed to load config, using defaults: %v", err)
		cfg = config.Default()
	}

	app := &App{}
	app.mgr = accounts.NewManager(cfg, func(name string, payload accounts.EventPayload) {
		// app.ctx isn't set until startup() runs (Wails calls it
		// after this constructor), but Init()/StartScheduledPolling
		// only run from startup() too, so ctx is always populated
		// by the time an event actually fires.
		runtime.EventsEmit(app.ctx, name, payload)

		if name == accounts.EventNeedsReauth {
			go sendReauthNotification(payload.Message)
		}

		// Recomputed on every event rather than special-cased to
		// EventNeedsReauth/EventReconnected, so it also stays correct
		// after a manual reauth from the UI and any other path that
		// changes an account's status. SetTrayErrorState no-ops
		// unless the overall state actually flips.
		SetTrayErrorState(app.mgr.NeedsAttention())
	})
	return app
}

// sendReauthNotification fires a native OS notification. Best-effort:
// a failure here (e.g. no notification server, permissions denied)
// only logs — it must never take down the poll cycle that triggered
// it.
func sendReauthNotification(body string) {
	n := toast.Notification{
		AppID: notifyAUMID,
		Title: "Kickoff Cloud Sync",
		Body:  body,
	}
	if err := n.Push(); err != nil {
		log.Printf("failed to send reauth notification: %v", err)
	}
}

// startup is called by Wails once the frontend is ready.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	logbuf.OnWrite(func(line string) {
		runtime.EventsEmit(a.ctx, "server-log", line)
	})

	pollCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	a.mgr.Init(pollCtx)
	SetTrayErrorState(a.mgr.NeedsAttention())
	a.mgr.StartScheduledPolling(pollCtx)

	go a.updateCheckLoop(pollCtx)
}

// updateCheckInterval is how often the app automatically checks for
// a newer release, beyond the one check at startup — long enough to
// be a non-issue for GitHub's API, short enough that a tray app left
// running for days still finds out about a new version reasonably
// promptly.
const updateCheckInterval = 24 * time.Hour

// updateCheckLoop checks once immediately, then on updateCheckInterval
// until ctx is done (app shutdown).
func (a *App) updateCheckLoop(ctx context.Context) {
	a.checkForUpdate()

	ticker := time.NewTicker(updateCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.checkForUpdate()
		}
	}
}

// checkForUpdate runs an automatic check (startup or the periodic
// recheck) — unlike CheckForUpdateNow, it suppresses the notice for
// a version the user already chose "skip this version" on. A failure
// (no network, GitHub unreachable, rate limited) is logged and
// otherwise ignored — this is a convenience notice, not something
// that should ever interrupt startup or look like an error.
func (a *App) checkForUpdate() {
	if _, err := a.runUpdateCheck(true); err != nil {
		log.Printf("update check failed: %v", err)
	}
}

// CheckForUpdateNow runs an update check immediately, bypassing the
// 24h schedule, for the "Check for updates" button in Settings. It
// always reports the real state — an explicit manual check ignores
// any previously skipped version, since the user is asking right now.
func (a *App) CheckForUpdateNow() (updatecheck.Info, error) {
	return a.runUpdateCheck(false)
}

// runUpdateCheck is the shared implementation behind checkForUpdate
// and CheckForUpdateNow. When respectSkip is true and the latest
// version is the one the user chose to skip, Available is forced to
// false before caching/emitting — so both GetUpdateInfo and the
// "update-available" event stay consistent with what should actually
// be shown.
func (a *App) runUpdateCheck(respectSkip bool) (updatecheck.Info, error) {
	info, err := updatecheck.Check(appVersion)
	if err != nil {
		return updatecheck.Info{}, err
	}

	if respectSkip && info.Available && info.LatestVersion == a.mgr.GetSkippedUpdateVersion() {
		info.Available = false
	}

	a.updateMu.Lock()
	a.updateInfo = info
	a.updateMu.Unlock()

	if info.Available {
		runtime.EventsEmit(a.ctx, "update-available", info)
	}
	return info, nil
}

// GetUpdateInfo returns the result of the most recent update check,
// or its zero value (Available: false) if none has completed yet.
func (a *App) GetUpdateInfo() updatecheck.Info {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	return a.updateInfo
}

// SkipUpdateVersion marks version as one to stop automatically
// notifying about (see config.Config.SkippedUpdateVersion).
func (a *App) SkipUpdateVersion(version string) error {
	return a.mgr.SetSkippedUpdateVersion(version)
}

// GetAppVersion returns this build's version — independent of
// GetUpdateInfo, which depends on a network round-trip that may not
// have completed (or may fail) yet.
func (a *App) GetAppVersion() string {
	return appVersion
}

// GetServerLogs returns everything currently buffered from the
// standard log package (see internal/logbuf) — up to the last 1000
// lines, oldest first. Combine with the "server-log" event for live
// updates after this initial snapshot.
func (a *App) GetServerLogs() []string {
	return logbuf.Lines()
}

func (a *App) shutdown(ctx context.Context) {
	if a.cancel != nil {
		a.cancel()
	}
}

// --- Methods below are bound to the frontend (callable from JS) ---
// One rectangle/box per account in the GUI maps directly onto
// AccountView below plus these action methods.

func (a *App) ListAccounts() []accounts.AccountView {
	return a.mgr.ListAccounts()
}

// BeginAddAccount is step 1 of 3 for adding a new account: it opens
// the Epic login page in the system browser and returns that same
// URL so the frontend can show it as a fallback link. There is no
// automatic callback for this login — after the user signs in, Epic
// redirects to a page whose body is a small JSON blob containing an
// "authorizationCode" field. The user copies that code out and
// pastes it into the app, then call SubmitAddAccountCode with it.
func (a *App) BeginAddAccount() string {
	url := auth.GetAuthURL()
	runtime.BrowserOpenURL(a.ctx, url)
	return url
}

// SubmitAddAccountCode is step 2: exchanges the manually-copied
// authorization code (see BeginAddAccount) for a live connection and
// the Epic display name. Returns a pending ID; follow up with
// ConfirmAddAccount once the user has entered a friendly name and
// ballchasing token.
func (a *App) SubmitAddAccountCode(authCode string) (accounts.PendingAccountView, error) {
	return a.mgr.SubmitAddAccountCode(a.ctx, authCode)
}

// ConfirmAddAccount is step 3: validates the ballchasing token and,
// if valid, persists the account. On error (invalid token), the
// pending login from SubmitAddAccountCode is preserved — call this
// again with a corrected token rather than restarting from
// BeginAddAccount.
func (a *App) ConfirmAddAccount(pendingID, ballchasingToken, friendlyName string) (accounts.AccountView, error) {
	return a.mgr.ConfirmAddAccount(a.ctx, pendingID, ballchasingToken, friendlyName)
}

// CancelAddAccount discards an in-progress add-account flow.
func (a *App) CancelAddAccount(pendingID string) {
	a.mgr.CancelAddAccount(pendingID)
}

// BeginReauth is step 1 of 2 for re-authenticating an existing
// account — same shape as BeginAddAccount, but for an account that
// already exists. Follow up with SubmitReauthCode.
func (a *App) BeginReauth(id string) (string, error) {
	if err := a.mgr.BeginReauth(id); err != nil {
		return "", err
	}
	url := auth.GetAuthURL()
	runtime.BrowserOpenURL(a.ctx, url)
	return url, nil
}

// SubmitReauthCode is step 2: exchanges the manually-copied
// authorization code (see BeginReauth) and updates the account in
// place.
func (a *App) SubmitReauthCode(id, authCode string) error {
	return a.mgr.SubmitReauthCode(a.ctx, id, authCode)
}

// CancelReauth reverts the "authenticating" status set by BeginReauth
// if the user backs out of the reauth dialog before submitting a code.
func (a *App) CancelReauth(id string) {
	a.mgr.CancelReauth(id)
}

func (a *App) PauseAccount(id string) error {
	return a.mgr.PauseAccount(id, true)
}

func (a *App) ResumeAccount(id string) error {
	return a.mgr.PauseAccount(id, false)
}

func (a *App) RemoveAccount(id string) error {
	return a.mgr.RemoveAccount(id)
}

func (a *App) SetAccountBallchasingToken(id, token string) error {
	return a.mgr.SetBallchasingToken(id, token)
}

func (a *App) SetFriendlyName(id, name string) error {
	return a.mgr.SetFriendlyName(id, name)
}

func (a *App) SetReplayVisibility(id, visibility string) error {
	return a.mgr.SetReplayVisibility(id, visibility)
}

func (a *App) PollAccountNow(id string) error {
	return a.mgr.PollAccountNow(a.ctx, id)
}

func (a *App) GetPollIntervalSecs() int {
	return a.mgr.GetPollIntervalSecs()
}

func (a *App) SetPollIntervalSecs(secs int) error {
	return a.mgr.SetPollIntervalSecs(secs)
}

func (a *App) GetHTTPTimeoutSecs() int {
	return a.mgr.GetHTTPTimeoutSecs()
}

func (a *App) SetHTTPTimeoutSecs(secs int) error {
	return a.mgr.SetHTTPTimeoutSecs(secs)
}

// GetGroupPrivateSeriesEnabled reports whether consecutive private
// matches against the same custom-named opponent get automatically
// bundled into a ballchasing group.
func (a *App) GetGroupPrivateSeriesEnabled() bool {
	return a.mgr.GetGroupPrivateSeriesEnabled()
}

func (a *App) SetGroupPrivateSeriesEnabled(enabled bool) error {
	return a.mgr.SetGroupPrivateSeriesEnabled(enabled)
}

// GetLaunchAtLogin reports whether the app is currently registered to
// start automatically when Windows logs in — read live from the
// registry (see internal/autostart), not a cached setting, so it
// can't drift out of sync with reality.
func (a *App) GetLaunchAtLogin() (bool, error) {
	return autostart.IsEnabled()
}

// SetLaunchAtLogin enables or disables starting automatically at
// Windows login. When enabled, the auto-launched instance starts
// hidden in the tray rather than popping up a window.
func (a *App) SetLaunchAtLogin(enabled bool) error {
	return autostart.SetEnabled(enabled)
}
