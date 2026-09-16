package main

import (
	"context"
	"log"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/yourusername/rl-replay-uploader/internal/accounts"
	"github.com/yourusername/rl-replay-uploader/internal/auth"
	"github.com/yourusername/rl-replay-uploader/internal/config"
)

type App struct {
	ctx     context.Context
	mgr     *accounts.Manager
	cancel  context.CancelFunc
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
	})
	return app
}

// startup is called by Wails once the frontend is ready.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	pollCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	a.mgr.Init(pollCtx)
	a.mgr.StartScheduledPolling(pollCtx)
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
