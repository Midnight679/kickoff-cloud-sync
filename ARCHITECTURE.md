# Architecture

Implementation notes for anyone modifying this codebase. For usage instructions, see [README.md](README.md).

## Overview

`internal/accounts/manager.go` owns everything account-related and is deliberately kept independent of Wails, so it's testable without a GUI. `app.go` is a thin binding layer exposing `Manager`'s methods to the frontend. The React app in `frontend/src` wires this up: `App.tsx` holds the account list/modal state, `api.ts` is a hand-typed wrapper around `window.go.main.App.*` (kept in sync with `app.go` by hand rather than via Wails codegen), and `components/` has one file per piece of UI.

Each account (`config.Account`) has its own:
- Epic refresh token (persisted; used to reconnect silently on startup or after a dropped connection — falls back to `needs_reauth` if it's expired)
- ballchasing.com API token
- `UploadedMatches` dedupe map
- `Paused` flag — skipped by the scheduled poll cycle, but can still be polled manually

Auth status (`authenticated` / `needs_reauth` / `authenticating`) is runtime-only, never persisted — it's recomputed at startup by attempting each account's saved refresh token silently.

## Epic authentication

There is no automated browser callback for Epic login — after the user signs in, Epic redirects to a page whose body is a small JSON blob containing an `authorizationCode` field, which the user has to copy out and paste back into the app.

**Adding an account (three steps):**
1. `BeginAddAccount()` — opens the Epic login URL in the system browser and returns that same URL as a fallback link.
2. `SubmitAddAccountCode(authCode)` — exchanges the pasted code for a live connection and the Epic display name, returning `{pending_id, epic_display_name}`. Nothing is persisted yet.
3. `ConfirmAddAccount(pendingID, ballchasingToken, friendlyName)` — validates the token against ballchasing's own API before saving. On failure, the pending login is preserved so the frontend can retry with a different token without repeating the browser login.

`CancelAddAccount(pendingID)` discards an in-progress flow at any point.

**Reauthenticating an existing account (two steps)**, same shape as steps 1-2 above: `BeginReauth(id)` opens the login URL, then `SubmitReauthCode(id, authCode)` completes it. `CancelReauth(id)` reverts the "authenticating" status if the user backs out before submitting a code — without it, the account would stay stuck showing "authenticating" until the next attempt or app restart.

### Silent reconnection

Psyonix allows only one active session per account on the connection this app uses (the same channel the real Rocket League client uses for party/friends/presence). Launching the actual game while this app holds a connection will close it with a `DuplicateLogin` error — this is expected, not a bug.

`Manager.ensureConnected` checks the connection before every poll and, if it's dropped, silently reconnects using the account's stored refresh token (the same mechanism used at app startup) rather than requiring a full browser-based reauth. Epic rotates the refresh token on every use, so the new one is persisted immediately after each reconnect.

One inherent trade-off: if the app's poll cycle fires while the real game client is actively connected, the app's reconnect will itself kick the game client's session (since whichever side logs in more recently wins). A longer poll interval reduces how often this can happen, though it can't eliminate it entirely — there's no reliable way to detect "an active match is in progress" without an existing connection to check it.

## Duplicate handling

ballchasing.com rejects true duplicate uploads server-side — uploading a replay it already has returns HTTP 409 with the existing replay's ID, which is treated the same as a successful 201. On top of that, each account's `UploadedMatches` map (`matchID -> ballchasing replay ID`) is checked before attempting any download/upload, since the poller has no "since last seen" cursor — every poll walks the full current match history list.

`UploadedMatches` has a hard size ceiling (`maxUploadedMatchesCacheBytes`, 100MB) as a safety net; if ever exceeded, the map resets and keeps only the most recent match, and a `cache-cleared` event fires.

## Replay visibility

Each `config.Account` has a `ReplayVisibility` field (`public`/`unlisted`/`private`). `Account.Visibility()` defaults it to `public` for any account with the field unset — including every account created before this setting existed, so no config migration is needed. It's read at upload time in both `handleMatch` and `retryPendingUploads` and passed straight through to `uploader.UploadReplay`. Changing it only affects future uploads; anything already uploaded keeps whatever visibility it was uploaded with.

## Failed-upload retry

If a replay downloads successfully but the ballchasing upload fails (network hiccup, ballchasing's daily upload quota, etc.), it's moved into a persistent `pending-uploads` directory instead of being lost. `retryPendingUploads` retries everything there once per poll cycle (scheduled or manual), using the account's *current* token, so fixing a bad token retroactively picks up anything that failed because of it.

While a replay is waiting in this cache, the normal per-match handler skips it entirely (rather than re-downloading and re-attempting the upload on every poll on top of the retry pass already doing so).

## Last-poll summary

`runtimeState.lastPoll` (a `*PollResult` — found/uploaded/failed counts) is scratch state for exactly one poll cycle, not history: `pollAccount` overwrites it in place every time an account is polled, whether or not anything happened, and it's never persisted. `ListAccounts` surfaces it as `AccountView.LastPoll`; the frontend (referred to as the "poll summary" line in the account card) renders it after every poll, including "0 found, 0 uploaded" — absent only before the first poll this run. "Uploaded" is colored green only when something was actually found and nothing failed; a "failed" line appears only when something did. `handleMatch`'s two-part return (`found`, `uploaded`) is what `pollAccount` aggregates into these counts — a match counts as `found` whenever it needed any attention this cycle (freshly detected, pending-retry still outstanding, or errored), and `uploaded` only when it ended in an actual successful upload; `failed` is just `found - uploaded`.

## ballchasing rate limiting

Confirmed against ballchasing's own API docs (https://ballchasing.com/doc/api): accounts without a Patreon tier are limited to 2 calls/second on `GET /replays/{id}` and `PATCH /replays/{id}` (1000/hour each), with a 429 response when exceeded. The upload endpoint's own doc page states no explicit number, but in practice a burst of new matches from one poll — each triggering an upload plus a `finalizeReplayTitle` goroutine that calls both of the limited endpoints — could still trip the limit.

`internal/uploader.ballchasingLimiter` is a single shared `golang.org/x/time/rate.Limiter` (1.5/sec, burst 1 — deliberately under the documented ceiling) that every outbound call in the package waits on via `doWithRetry`, regardless of which one of `ValidateToken`/`UploadReplay`/`GetReplay`/`SetReplayTitle` is calling. `doWithRetry` takes a request *factory* rather than a built request, since a request with a body (the multipart upload, the JSON title patch) can only be sent once — each retry attempt needs its own fresh one. A 429 triggers up to 3 retries with doubling backoff (2s, 4s, 8s) before giving up; anything else is returned immediately. Covered by `internal/uploader/ballchasing_test.go` against a real `httptest.Server`, not just reasoned about.

## Events

Emitted via `runtime.EventsEmit`, payload is `EventPayload` (`account_id`, optional `match_id`/`message`, and `manual` — true if triggered by an explicit "Poll Now" click rather than the scheduled cycle):

| Event | Meaning |
|---|---|
| `accounts-changed` | Frontend should re-call `ListAccounts` |
| `match-detected` | A new match was found |
| `upload-complete` | Upload succeeded |
| `upload-error` | Upload failed (may be retried later from the pending-uploads cache) |
| `auth-error` | The connection or reconnect attempt failed |
| `no-matches` | Poll succeeded but history had zero entries |
| `reconnected` | A dropped connection (e.g. `DuplicateLogin`) was silently re-established |
| `cache-cleared` | The dedupe cache hit its size cap and was reset |
| `needs-reauth` | Fired once per transition into `needs_reauth` (not on every retry) — drives both the OS toast notification and the tray error indicator below |

## Tray error indicator

`Manager.NeedsAttention()` reports whether any account currently has `StatusNeedsReauth` — the same condition the frontend uses to turn the settings gear red. `app.go`'s event-emission callback recomputes this after *every* event, not just reauth-related ones, so it also stays correct after a manual reauth from the UI or any other path that changes an account's status. `SetTrayErrorState` (`tray.go`) then swaps the tray icon between the normal and error-badged `.ico` — both embedded via `go:embed` — but only calls into the tray library when the state actually flips, since that involves writing a temp file.

## Update checking

`internal/updatecheck.Check` compares `appVersion` (a hand-maintained constant in `app.go` — bump it alongside `wails.json`'s `productVersion` and the git tag) against GitHub's `releases/latest` API for this repo, doing numeric dot-separated comparison rather than a string one (`"0.1.10" > "0.1.9"` would be backwards as a plain string compare). It only ever reads GitHub's API — nothing is downloaded or installed automatically.

`App.updateCheckLoop` runs this once at startup and then every `updateCheckInterval` (24h) for as long as the app runs, via a `time.Ticker` tied to the same `pollCtx` the account poll cycle uses, so it stops cleanly on shutdown. `App.runUpdateCheck(respectSkip bool)` is the shared implementation behind both the automatic loop and the manual "Check for updates" button in Settings (`CheckForUpdateNow`): automatic checks pass `respectSkip: true` and suppress the notice entirely (no cached `Info`, no `update-available` event) when the latest version matches `config.Config.SkippedUpdateVersion`; the manual button passes `false` and always reports the real state, since an explicit check is the user asking right now regardless of an earlier skip decision.

The frontend's "Skip this version" button persists the skip via `SkipUpdateVersion` (routed through `Manager`, the sole owner of `config.json`, even though this isn't account data — the same pattern `PollIntervalSecs`/`HTTPTimeoutSecs` already use). "Dismiss" is deliberately *not* persisted — it's just React state (`dismissedVersion`) that hides the banner for the rest of this session, and resets on the next launch or if a newer version than the dismissed one shows up.

## Packaging and uninstall

The Windows installer (`build/windows/installer/project.nsi`, built via `wails build --nsis --installscope user`) installs per-user — no admin/UAC needed — and creates Start Menu and desktop shortcuts using the icons in `build/windows/`.

Uninstalling always kills any running instance first (the app hides to the tray rather than quitting on close, so it may still be running) and removes the "launch at startup" registry entry if one was ever set. It then asks whether to also remove local data; if so, it runs the app with a `--purge` flag before deleting anything else. `purge.go`'s `purgeAllData` deletes every configured account's OS-keyring credentials (via the existing `secrets.Delete`) and the app's entire `%APPDATA%\kickoff-cloud-sync` directory, located via `config.AppDataDir()`. That function is safety-checked — it must resolve under the OS's own per-user config directory, must be absolute, and its base name must literally be `kickoff-cloud-sync` — so it can never resolve to, and `purgeAllData` can never delete, anything outside the app's own data.

## Reliability details

- **Poll overlap guard** — `Manager.polling` tracks which accounts are mid-poll, so a manual "Poll Now" click can't race the scheduled cycle for the same account.
- **Config save race** — all writers go through `Manager.persist()`, which re-reads the freshest in-memory config right before writing and serializes disk writes through a dedicated mutex. `config.Save` itself writes atomically (temp file + rename).
- **Configurable network timeout** — one shared `*http.Client` used by every outbound call, swapped atomically when the timeout setting changes, so a single hung request can't stall the whole poll cycle.
- **Credential storage** — Epic refresh tokens and ballchasing tokens live in the OS's native credential store (`zalando/go-keyring`), keyed by account ID. `config.Account` holds no secrets, only a `HasBallchasingToken` flag. This depends on a keyring service being available; on a Linux session without one running, credential operations return `secrets.ErrUnavailable` rather than silently falling back to plaintext.
