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

## Failed-upload retry

If a replay downloads successfully but the ballchasing upload fails (network hiccup, ballchasing's daily upload quota, etc.), it's moved into a persistent `pending-uploads` directory instead of being lost. `retryPendingUploads` retries everything there once per poll cycle (scheduled or manual), using the account's *current* token, so fixing a bad token retroactively picks up anything that failed because of it.

While a replay is waiting in this cache, the normal per-match handler skips it entirely (rather than re-downloading and re-attempting the upload on every poll on top of the retry pass already doing so).

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

## Reliability details

- **Poll overlap guard** — `Manager.polling` tracks which accounts are mid-poll, so a manual "Poll Now" click can't race the scheduled cycle for the same account.
- **Config save race** — all writers go through `Manager.persist()`, which re-reads the freshest in-memory config right before writing and serializes disk writes through a dedicated mutex. `config.Save` itself writes atomically (temp file + rename).
- **Configurable network timeout** — one shared `*http.Client` used by every outbound call, swapped atomically when the timeout setting changes, so a single hung request can't stall the whole poll cycle.
- **Credential storage** — Epic refresh tokens and ballchasing tokens live in the OS's native credential store (`zalando/go-keyring`), keyed by account ID. `config.Account` holds no secrets, only a `HasBallchasingToken` flag. This depends on a keyring service being available; on a Linux session without one running, credential operations return `secrets.ErrUnavailable` rather than silently falling back to plaintext.
