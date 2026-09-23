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

## Private match series grouping

`Manager.assignReplayGroup` bundles consecutive private-match uploads from the same account into a ballchasing.com group when both teams have a custom lobby name (e.g. "POLAR BEARS vs WHISKER GOBLINS") — the same custom-name detection `uploader.BuildTitle` already does for titles. "Consecutive" means same-account chronological adjacency, not a time window: a non-private upload, or a private one with a different team pair (order-insensitive — `uploader.CustomTeamPair` sorts it, so the teams swapping blue/orange sides between games doesn't break the streak), resets the tracked pair.

Grouping is retroactive rather than eager: the first match of a new pair is only tracked (`runtimeState.lastPrivateTeamPair`/`lastPrivateReplayID`), not grouped. Only once a *second* consecutive match confirms the same pair is a group actually created (`uploader.CreateGroup`) and both replays patched into it (`uploader.SetReplayGroup`) — so a one-off opponent never gets a group of its own. This state is ephemeral runtime state per account, like `lastPoll`, not persisted; a restart mid-series just starts a fresh streak on the next private match.

Team names for a private match are only known after ballchasing's own post-upload parse, so group assignment happens from the same `finalizeReplay` hook that sets the title, not at upload time — a `group` upload-time parameter exists on ballchasing's API but isn't usable here for that reason.

The whole feature is gated by `config.Config.DisableGroupPrivateSeries` (inverted, so old configs and the zero value both mean "on") — exposed as a checkbox in Settings via `GetGroupPrivateSeriesEnabled`/`SetGroupPrivateSeriesEnabled`.

## Failed-upload retry

If a replay downloads successfully but the ballchasing upload fails, what happens next depends on whether ballchasing's response means "try again" or "this file specifically is never going to work":

- **Temporary** (network hiccup, ballchasing's daily upload quota, a 5xx, a bad token) — moved into a persistent `pending-uploads` directory instead of being lost. `retryPendingUploads` retries everything there once per poll cycle (scheduled or manual), using the account's *current* token, so fixing a bad token retroactively picks up anything that failed because of it.
- **Permanent** (`uploader.UploadError.Permanent()` — a 4xx other than 401/403/429, meaning ballchasing rejected the replay file itself) — moved into a separate `failed-uploads` directory instead, retried at most once every 24 hours by `runFailedUploadsRetryLoop` rather than every poll cycle. Retrying a file ballchasing has already rejected outright on every single cycle forever (potentially every 10 minutes, the minimum poll interval) accomplishes nothing but burning API calls and filling the log — but it's not given up on entirely, since a 400 isn't always truly permanent (ballchasing's parser occasionally rejects very recently-patched replays until it's updated, and would accept the same file a few days later).

Both directories use the same `pendingFileSeparator`-joined filename (`accountID__matchID.replay`), and both are skipped by the normal per-match handler (`hasPendingReplay`/`hasFailedUpload`) so a cached replay is never redundantly re-downloaded and re-attempted outside its own retry pass. `retryPendingUploads` and `retryFailedUploads` share one iterator, `forEachCachedFile`, which does the directory scan, the `m.polling` guard (the same map `pollAccount` uses, so none of a manual poll, the scheduled pending-uploads pass, and the daily failed-uploads pass can race the same account's upload at once), and the account-state snapshot — each caller only supplies its own per-file handler (`attemptCachedUpload`, plus success/failure bookkeeping specific to that queue). `m.polling` is always released via `defer` inside the iterator, not a plain statement after the handler returns — a panic mid-file used to leave an account's guard stuck set, permanently locking it out of both queues until restart.

`failed-uploads` is capped at `config.Config.FailedUploadsMaxCount` (`GetFailedUploadsMaxCount`/`SetFailedUploadsMaxCount` in Settings; defaults to `DefaultFailedUploadsMaxCount`, 100, when unset — enforced minimum of 1) — past that, the single oldest one (by when it moved there, not when the match was played) is deleted to make room for a new permanent failure, so a persistently-broken account can't grow this folder without bound. `moveToFailedUploads`'s evict-then-write sequence is serialized process-wide by a dedicated `Manager.failedUploadsMu`, separate from the per-account `m.polling` guard — eviction needs a consistent view of the whole directory, and two different accounts permanently failing at the same instant would otherwise both read the same pre-eviction snapshot and let the cap drift upward.

`runFailedUploadsRetryLoop` targets `config.Config.FailedUploadsRetryHour` (0-23 local, `GetFailedUploadsRetryHour`/`SetFailedUploadsRetryHour`; defaults to `DefaultFailedUploadsRetryHour`, 4am, when unset — a pointer field since 0/midnight is itself a valid choice, not just an unset sentinel) rather than a plain 24h ticker from startup — late enough that active gaming is rare, but a machine left on for a background uploader is realistically still running. If that window was missed entirely (the computer was off or asleep through it), `nextFailedUploadsRetryWait` catches up immediately once next checked rather than waiting for the following day's window, so "at least once every 24 hours" holds even then; the target hour is a preference, not a hard requirement. `SetFailedUploadsRetryHour` and the manual "Retry now" button (`RetryFailedUploadsNow`) both signal `failedUploadsRetryChanged` to interrupt the loop's current wait, the same way `intervalChanged` wakes the main poll loop (see "Poll interval takes effect immediately" below) — a changed hour, or a manual run that just happened, takes effect immediately rather than waiting for the loop's next natural wake-up.

## Last-poll summary

`runtimeState.lastPoll` (a `*PollResult` — found/uploaded/failed counts) is scratch state for exactly one poll cycle, not history: `pollAccount` overwrites it in place every time an account is polled, and it's never persisted. `ListAccounts` surfaces it as `AccountView.LastPoll`; the frontend (referred to as the "poll summary" line in the account card) renders it after every poll, including "0 found, 0 uploaded" — absent only before the first poll this run. "Uploaded" is colored green only when something was actually found and nothing failed; a "failed" line appears only when something did. `handleMatch`'s two-part return (`found`, `uploaded`) is what `pollAccount` aggregates into these counts — a match counts as `found` whenever it needed any attention this cycle (freshly detected, pending-retry still outstanding, or errored), and `uploaded` only when it ended in an actual successful upload; `failed` is just `found - uploaded`.

Two cases this deliberately handles rather than just falling out of the above: `pollAccount` is seeded with however many uploads `retryPendingUploads` already completed for this account earlier in the same cycle, since by the time `handleMatch` runs its own dedupe check those matches are already in `UploadedMatches` and would otherwise look like they needed no attention at all. And `lastPoll` is only overwritten when the match-history fetch actually succeeded — on failure it's left as whatever the last successful poll reported, since replacing it with a fresh, clean `0/0/0` would be indistinguishable from a quiet successful poll (the `auth-error` event is what actually surfaces the failure).

## ballchasing rate limiting

Confirmed against ballchasing's own API docs (https://ballchasing.com/doc/api): accounts without a Patreon tier are limited to 2 calls/second on `GET /replays/{id}` and `PATCH /replays/{id}` (1000/hour each), with a 429 response when exceeded. The upload endpoint's own doc page states no explicit number, but in practice a burst of new matches from one poll — each triggering an upload plus a `finalizeReplay` goroutine that calls the title/group endpoints (`GetReplay`, `SetReplayTitle`, and for a private-match series, `CreateGroup`/`SetReplayGroup` — see [Private match series grouping](#private-match-series-grouping)) — could still trip the limit.

`internal/uploader.ballchasingLimiter` is a single shared `golang.org/x/time/rate.Limiter` (1.5/sec, burst 1 — deliberately under the documented ceiling) that every outbound call in the package waits on via `doWithRetry`, regardless of which one of `ValidateToken`/`UploadReplay`/`GetReplay`/`SetReplayTitle` is calling. `doWithRetry` takes a request *factory* rather than a built request, since a request with a body (the multipart upload, the JSON title patch) can only be sent once — each retry attempt needs its own fresh one. A 429, or a transport-level error from `Do` itself (see below), triggers up to 3 retries with doubling backoff (2s, 4s, 8s) before giving up. Covered by `internal/uploader/ballchasing_test.go` against a real `httptest.Server` (including a hijacked-and-closed connection to simulate the transport-error case), not just reasoned about.

### Stale keep-alive connections

Observed in practice: a burst of uploads after an idle stretch (the account was just playing, not making any ballchasing calls) can fail several requests in a row with a transport-level error like `wsarecv: An existing connection was forcibly closed by the remote host`, then succeed. This is a pooled keep-alive connection ballchasing's server (or something in front of it) closed while it sat idle — Go's `http.Client` won't silently retry a non-idempotent method like `POST` on a connection that turns out to be stale, so it surfaces as a hard failure instead of transparently reconnecting.

Two layers address this:
- **Prevention** — `internal/httpclient`'s shared `http.Transport` sets `IdleConnTimeout` to 30s, well under Go's own 90s default, so idle connections are proactively retired client-side before a real-world reverse proxy or CDN's shorter idle timeout has a chance to close them first and cause the race.
- **Backstop** — `doWithRetry` retries on a transport-level error the same as a 429 (see above), so on the rare occasion prevention doesn't cover it (the server's actual timeout turns out to be even shorter, or a plain one-off network blip), a single stale connection costs one extra attempt instead of failing the whole upload — which would otherwise only recover on the next poll cycle's `pending-uploads` retry, 10+ minutes later.

## Events

Emitted via `runtime.EventsEmit`, payload is `EventPayload` (`account_id`, optional `match_id`/`message`, and `manual` — true if triggered by an explicit "Poll Now" click rather than the scheduled cycle):

| Event | Meaning |
|---|---|
| `accounts-changed` | Frontend should re-call `ListAccounts` |
| `match-detected` | A new match was found |
| `upload-complete` | Upload succeeded — also drives an optional OS toast notification (`sendUploadNotification`, off by default) when `NotifyOnUploadComplete` is set |
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

## Settings export/import

`Manager.ExportSettings`/`ImportSettings` (`internal/accounts/settings_export.go`) round-trip a deliberately narrow subset of `Config` as JSON: every global setting, plus each account's non-secret preferences (`FriendlyName`, `ReplayVisibility`, `Paused`). Tokens and refresh tokens are never included — they live only in the OS keyring (see `internal/secrets`), and an account needs a fresh Epic sign-in on any machine regardless of what's in this file, so there'd be nothing meaningful to restore for them anyway.

Account preferences are matched back up on import by `EpicAccountID`, not the account's own `ID` — this app's own IDs are generated fresh every time an account is added, so they never survive a remove-and-re-add, but `EpicAccountID` (Epic's own stable identifier) does. An account with no `EpicAccountID` (added before that field existed, or never completed a login) is skipped on export, since it has nothing to match against. On import, an entry whose `EpicAccountID` doesn't match any currently-configured account is counted in `ImportSettingsResult.AccountsSkipped` rather than erroring — normal right after a fresh install, before those accounts are re-added.

Global settings apply through the exact same setters (`SetPollIntervalSecs`, `SetGroupPrivateSeriesEnabled`, etc.) that Settings' UI calls, so the same validation and side effects (e.g. waking the poll loop on a changed interval) apply on import too — there's no separate "bulk apply" path to keep in sync. `App.ExportSettings`/`ImportSettings` (`app.go`) are thin wrappers that add the native save/open file dialog (`runtime.SaveFileDialog`/`OpenFileDialog`) and the actual file I/O; `ImportSettings` returns a `*accounts.ImportSettingsResult` rather than a plain struct specifically so the frontend can tell "user cancelled the dialog" (`nil`) apart from a real result of zero matches and zero skips (e.g. a settings-only export with no account entries at all).

## Packaging and uninstall

The Windows installer (`build/windows/installer/project.nsi`, built via `wails build --nsis --installscope user`) installs per-user — no admin/UAC needed — and creates Start Menu and desktop shortcuts using the icons in `build/windows/`.

Uninstalling always kills any running instance first (the app hides to the tray rather than quitting on close, so it may still be running) and removes the "launch at startup" registry entry if one was ever set. It then asks whether to also remove local data; if so, it runs the app with a `--purge` flag before deleting anything else. `purge.go`'s `purgeAllData` deletes every configured account's OS-keyring credentials (via the existing `secrets.Delete`) and the app's entire `%APPDATA%\kickoff-cloud-sync` directory, located via `config.AppDataDir()`. That function is safety-checked — it must resolve under the OS's own per-user config directory, must be absolute, and its base name must literally be `kickoff-cloud-sync` — so it can never resolve to, and `purgeAllData` can never delete, anything outside the app's own data.

## Reliability details

- **Poll overlap guard** — `Manager.polling` tracks which accounts are mid-poll (or mid-file in one of the retry passes, via `forEachCachedFile`), so a manual "Poll Now" click, the scheduled cycle, and both retry passes can never step on each other for the same account. It deliberately means two different things depending on who set it — "pollAccount is using this account's `*rlapi.PsyNetRPC`" or "a retry pass is uploading a cached file that never touches the RPC at all" — so `RemoveAccount`/`SubmitReauthCode` check a separate, narrower `Manager.rpcInUse` (set only by `pollAccount`) to decide whether it's safe to close the old connection themselves. Checking `m.polling` there instead used to leak a connection whenever a retry pass, not `pollAccount`, happened to be mid-file for an account the moment it was removed or reauthenticated — `pollAccount` (the only thing that would otherwise notice and close the orphan) was never actually running for it.
- **Poll interval takes effect immediately** — `StartScheduledPolling`'s wait (`waitForNextCycle`) is a `time.Timer` selected alongside `Manager.intervalChanged`, which `SetPollIntervalSecs` signals (non-blocking) whenever the interval changes. Without this, shortening a long interval would do nothing until whatever wait was already in progress finished on its own.
- **Config save race** — all writers go through `Manager.persist()`, which re-reads the freshest in-memory config right before writing and serializes disk writes through a dedicated mutex. The snapshot is a deep copy (`config.Config.Clone()`), not a plain struct copy — a shallow copy would still share every account's `UploadedMatches` map with the live config, and marshalling it after the lock is released could race a concurrent map write into a fatal `concurrent map iteration and map write`. `config.Save` itself writes atomically (temp file + `fsync` + rename), and keeps an unreadable existing file aside (`config.json.corrupt-<unix time>`) rather than letting a failed load's fallback-to-defaults silently overwrite it.
- **Configurable network timeout** — one shared `*http.Client` used by every outbound call, swapped atomically when the timeout setting changes, so a single hung request can't stall the whole poll cycle. The same setting also bounds the Epic/PsyNet login chain (`internal/auth.withTimeout`), which otherwise ignores context entirely — rlapi's calls take none — and could hang the shared poll cycle behind a silent-reconnect attempt with no way to time out. `isTransientNetErr` (the check deciding whether a failed reconnect should flag `needs_reauth`) also recognizes `context.Canceled` specifically, not just `net.Error` — `withTimeout` can return one wrapped when app shutdown cancels the poll context mid-login, which otherwise looked identical to a real rejection and fired a spurious reauth notification on the way out.
- **Startup status** — every account is set to `authenticating` (not left with no runtime entry at all) before `Init`'s login loop starts attempting them one by one; `ListAccounts`/`NeedsAttention` also default a missing runtime entry to `authenticating` rather than `needs_reauth`. Otherwise, with a few accounts configured, every card and the tray gear would show red for the several seconds Init takes to get through all of them, before anything has actually failed.
- **Replay download hardening** — `matches.DownloadReplay` caps the response at `maxReplayBytes` (erroring out rather than silently truncating past it) and only proceeds for a host that isn't loopback, unspecified, private, or link-local (`isDisallowedReplayHost`, checked again on every redirect hop via `CheckRedirect`) — stronger than an explicit blocklist of specific spellings like `127.0.0.1`, which a literal like `127.0.0.2` or `0.0.0.0` would just walk past. A DNS name (not an address literal) is resolved and every address it points to is checked the same way, rather than being waved through unconditionally just for not being a literal — this doesn't defend against a second rebinding between this check and the actual connection moments later (closing that fully would need a custom `Dialer`), a residual gap accepted given the real caller here is always Epic's own API, not a user-supplied URL.
- **Windows autostart truthfulness** — `autostart.IsEnabled` also reads `StartupApproved\Run`, not just the `Run` key itself: turning an entry off via Task Manager's Startup tab leaves the `Run` key in place and records the disabled state there instead, which a check of `Run` alone would miss entirely. `SetEnabled(true)` clears that same flag (touching only its documented first byte, leaving the rest of the value's opaque bytes alone) when re-enabling from the app — without this, re-enabling after a Task-Manager-disable would write the `Run` key successfully with no error, but the checkbox would silently revert right after, since `IsEnabled` still saw the stale disable flag.
- **Upload counter survives the dedupe-cache safety net** — `Manager.TotalUploadedCount` (the window-title "N replays uploaded" counter) is backed by `config.Config.TotalUploadsEver`, a monotonic counter incremented once per successful upload, rather than derived by summing live `UploadedMatches` map sizes — summing live maps would make the counter visibly decrease whenever `resetCacheIfOversized`'s 100MB safety net truncates an account's map. `NewManager` backfills `TotalUploadsEver` from the sum of existing `UploadedMatches` the first time it's zero (a config saved before this field existed, or a genuinely fresh one, where the sum is 0 anyway) — exact for any account that hasn't hit that truncation, which in practice is every account.
- **Credential storage** — Epic refresh tokens and ballchasing tokens live in the OS's native credential store (`zalando/go-keyring`), keyed by account ID. `config.Account` holds no secrets, only a `HasBallchasingToken` flag. This depends on a keyring service being available; on a Linux session without one running, credential operations return `secrets.ErrUnavailable` rather than silently falling back to plaintext.
