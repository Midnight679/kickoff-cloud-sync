# RL Replay Uploader (Kickoff Cloud Sync)

**Version 0.1.3**

A system-tray app that watches recent Rocket League matches across
multiple accounts and auto-uploads replays to
[ballchasing.gg](https://ballchasing.com).

**Status: backend verified, working React frontend.** Every
third-party dependency call (`dank/rlapi`, ballchasing.com's API,
`go-keyring`, `systray`) has been checked against real, downloaded
source rather than guessed from docs — see
[Version history](#version-history) below. The full app (Go backend +
React/Vite frontend) builds clean and has been run end-to-end with
`wails dev`, including a real (failing, as expected without a valid
code) call to Epic's live OAuth API. What's still missing: a real tray
icon asset (currently text-only tooltip) and visual polish.

## How it works

1. Add one or more accounts via Epic's browser-based OAuth flow
   (`dank/rlapi`'s EGS login). On that login screen, choosing "Log in
   with Steam" (or Xbox/PlayStation/Nintendo) authenticates the same
   linked Epic identity — so this single flow covers Epic, Steam, and
   console-linked accounts, with no Steamworks SDK involved.
2. One shared poll cycle (`PollIntervalSecs`, global — see
   `internal/accounts/manager.go`) checks every non-paused,
   authenticated account's match history in turn. Each account also
   has its own "Poll Now" button for an on-demand check regardless of
   its paused state.
3. For each match, skip anything already recorded as uploaded for
   that specific account (see Duplicate handling below).
4. Download the replay directly from the cloud URL included in the
   match history response.
5. Upload it to ballchasing.gg using that account's own token.

**No local Rocket League install required.** Match history and replay
downloads both come from Epic/Psyonix's cloud via the API — confirmed
against `dank/rlapi` v0.1.23's actual source (`rpc.GetMatchHistory`
returns `[]MatchEntry`, each with a `Match.MatchGUID` and a
`ReplayUrl`). There is intentionally no fallback to a local `Demos`
folder, so this can run on any machine with network access, including
one that's never had Rocket League installed.

One caveat worth keeping in mind: the cloud copy is almost certainly
retention-limited to recent matches (same window as the in-game
"Recent Matches" list — the most recent 20). At the default 5-minute
poll interval this has a huge safety margin.

## Multi-account design

`internal/accounts/manager.go` owns everything account-related and is
deliberately kept independent of Wails, so it's testable without a
GUI. `app.go` is just a thin binding layer exposing its methods to
the frontend.

Each account (`config.Account`) has its own:
- Epic refresh token (persisted, used to skip the browser login on
  every app restart — falls back to `needs_reauth` status if it's
  expired)
- ballchasing.com API token (per-account, as discussed — this means
  multiple RL accounts can upload to different ballchasing profiles,
  or all to the same one if you paste the same token into each)
- `UploadedMatches` dedupe map
- `Paused` flag — a paused account is skipped by the shared poll
  cycle entirely but can still be polled manually

Auth status (`authenticated` / `needs_reauth` / `authenticating`) is
runtime-only, never persisted — it's recomputed at startup by
attempting each account's saved refresh token silently. A failed
attempt doesn't pop a browser window on its own; that only happens
when the user clicks Reauth.

### Frontend contract

One box per account, populated from `App.ListAccounts()`
(`[]accounts.AccountView`): display name, friendly name, auth
status, last/next poll time, paused flag, and whether a token is set
(without exposing the token itself). Actions:
`PauseAccount`/`ResumeAccount`, `RemoveAccount`,
`SetAccountBallchasingToken`, `SetFriendlyName`, `PollAccountNow`.
Global: `GetPollIntervalSecs`/`SetPollIntervalSecs`.

**There is no automated browser callback for Epic login** — after the
user signs in, Epic redirects to a page whose body is a small JSON
blob containing an `authorizationCode` field, which the user has to
copy out and paste back into the app. This shapes both the
add-account and reauth flows below.

**Adding an account is a three-step flow:**
1. `BeginAddAccount()` — opens the Epic login URL in the system
   browser and returns that same URL (so the frontend can also show
   it as a fallback link/button).
2. `SubmitAddAccountCode(authCode)` — exchanges the user's pasted
   code for a live connection and the Epic display name, returning
   `{pending_id, epic_display_name}`. Nothing is persisted yet.
3. `ConfirmAddAccount(pendingID, ballchasingToken, friendlyName)` —
   validates the token against ballchasing's own API (a real ping,
   not just a non-empty check) before saving anything. On failure,
   the pending login from step 2 is preserved server-side, so the
   frontend can just let the user retry with a different token
   rather than going through the browser login again.
4. `CancelAddAccount(pendingID)` — discards an in-progress flow at
   any point (e.g. the user backs out of the token step).

**Reauthenticating an existing account is a two-step flow**, same
shape as steps 1-2 above: `BeginReauth(id)` opens the login URL, then
`SubmitReauthCode(id, authCode)` completes it and updates the account
in place.

Friendly names show in parentheses next to the Epic display name —
`DisplayName (FriendlyName)` — computed client-side; the backend
returns both fields separately.

Events (`runtime.EventsEmit`, payload is `accounts.EventPayload` with
an `account_id` field so the frontend can route to the right box):
`accounts-changed`, `match-detected`, `upload-complete`,
`upload-error`, `auth-error`, `cache-cleared`, `no-matches` (poll
succeeded but history had zero entries — distinct from an auth error,
so the log can tell "nothing happened" apart from "something broke").
The React app in
`frontend/src` wires all of this up — `App.tsx` holds the account
list/modal state, `api.ts` is a hand-typed wrapper around
`window.go.main.App.*` (kept in sync with `app.go` by hand rather than
via wails codegen), and `components/` has one file per piece of UI
(account card, add-account wizard, reauth dialog, settings bar, event
log).

## Duplicate handling

ballchasing.com already rejects true duplicate uploads server-side —
uploading a replay it already has returns HTTP 409 with the existing
replay's ID, and `uploader.UploadReplay` treats that the same as a
successful 201. So duplicates never create extra entries on
ballchasing even without any local tracking.

On top of that, each account's `UploadedMatches` map (`matchID ->
ballchasing replay ID`) is checked *before* attempting any
download/upload. This matters because the poller has no "since last
seen" cursor — every poll (scheduled or manual) walks the full
current match history list, so the same matches keep showing up until
they age out of the cloud retention window. The local skip-list is
what actually stops re-uploads, and it also avoids uploading the full
file just to get a 409 back.

### Cache size cap

Each account's `UploadedMatches` map grows for the account's entire
lifetime in the app — nothing prunes old entries individually, since
a stale entry costs nothing at lookup time (a Go map lookup for an
ID that will never reappear in match history again is exactly as
cheap as a fresh one) and at roughly 80 bytes/entry, realistic usage
stays well under a megabyte for years.

Rather than leave it fully unbounded, though, there's a hard ceiling:
`maxUploadedMatchesCacheBytes` in `internal/accounts/manager.go`,
set to **100MB**. After each successful upload, the account's map is
JSON-serialized and measured; if it's over the cap, the whole map is
reset and restarted fresh, keeping only the match that was just
uploaded (so that one doesn't immediately look "new" again on the
very next poll). An `EventCacheCleared` event fires when this
happens, carrying the account ID, so the frontend can surface it if
you want — at 80 bytes/entry this is roughly 1.3 million matches per
account, so in practice it should never trigger, but the ceiling
exists per your request rather than trusting that math forever.

## Reliability fixes (post-review)

A few things added after a full sanity-check pass of the codebase:

- **Poll overlap guard** — `Manager.polling` tracks which accounts
  are mid-poll. A manual "Poll Now" click while the scheduled cycle
  is already polling that same account is rejected with an error
  rather than letting both run and both attempt the same
  download/upload.
- **Config save race** — all writers now go through `Manager.persist()`,
  which re-reads the freshest in-memory config right before writing
  and serializes every disk write through a dedicated mutex, so two
  near-simultaneous changes can't clobber each other. `config.Save`
  itself also writes atomically (temp file + rename).
- **Failed-upload retry cache** — if a replay downloads successfully
  but the ballchasing upload fails, it's moved into a persistent
  `pending-uploads` directory (`config.PendingUploadsDir()`) instead
  of being lost. `internal/accounts/pending_uploads.go` retries
  everything in there once per poll cycle (and once on a manual
  poll), using each account's *current* token — so fixing a bad
  token retroactively picks up anything that failed because of it.
  Successful uploads clean up their temp file; a pending file for an
  account that no longer exists, or a match that got uploaded some
  other way, gets cleaned up too.
- **Configurable network timeout** — `internal/httpclient` holds one
  shared `*http.Client` used by every outbound call (download,
  upload, token validation), swapped atomically whenever
  `SetHTTPTimeoutSecs` is called. Defaults to 30s
  (`config.Default().HTTPTimeoutSecs`), minimum 5s. Without this, a
  single hung request could stall the whole poll cycle indefinitely,
  since accounts are polled one at a time.
- **Tray Quit** now actually calls `runtime.Quit()` rather than just
  stopping the poll loop.
- **Credential storage** — `internal/secrets` stores each account's
  Epic refresh token and ballchasing API token in the OS's native
  credential store (`zalando/go-keyring`: Windows Credential Manager,
  macOS Keychain, Linux Secret Service), keyed by account ID.
  `config.Account` holds no secrets at all anymore — just a
  `HasBallchasingToken bool` flag for the UI to check without a
  keyring round-trip on every `ListAccounts` call. `RemoveAccount`
  cleans up the keyring entry too.
  **Known caveat**: this depends on a keyring service being
  available. On most Linux desktop sessions that's fine (GNOME
  Keyring / KWallet), but a session without one running (some
  minimal window managers, some remote/headless setups) will get
  `secrets.ErrUnavailable` from every token operation. There's no
  silent plaintext fallback — that failure needs to surface to the
  user rather than quietly writing tokens to disk unprotected.
- **Frontend input preservation** — `refreshAccounts()` in the
  reference frontend now snapshots in-progress input values (and
  which one has focus) before its full rebuild, and restores them
  after — otherwise typing into a token field mid-poll-cycle would
  get wiped out from under the user, since `accounts-changed` fires
  after every poll.

## Setup

### 1. Dependencies

```
go mod tidy
```

`go.mod` is pinned to real, resolvable versions (`dank/rlapi` v0.1.23,
Wails v2.16.0, `systray` v1.2.2) — this needs internet access to fetch
them from the module proxy, but no `git` binary is required for that
(everything resolves through `proxy.golang.org`).

### 2. Running it

The frontend is a real React + TypeScript + Vite app under
`frontend/`. Install the Wails CLI once, then:

```
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails dev
```

`wails dev` installs frontend npm dependencies automatically, starts
the Vite dev server, and opens the app window. It also serves a
browser-accessible mirror at `http://localhost:34115` — useful for
inspecting the UI without the native window, though `window.go` calls
only work there too because Wails' dev server bridges them the same
way.

### 3. Auth

`internal/auth/epic.go` opens Epic's login page in your browser for
each account you add. Pick whichever identity provider matches how
you normally sign in (Epic account, or "Log in with Steam") on that
screen — no SDK downloads, no DLLs. After logging in, Epic redirects
to a page showing a small JSON blob — copy the `authorizationCode`
value out of it and paste it into the app to finish adding the
account (see "Frontend contract" above for the exact method calls).

### 4. Ballchasing tokens

Get an API token from each account's own ballchasing.com account
settings, and paste it into that account's box once it's added.
Stored in the OS credential store (Windows Credential Manager /
macOS Keychain / Linux Secret Service) via `internal/secrets` —
never in `config.json` as plaintext. See "Credential storage" below.

## Testing

Recommended order, matching what we discussed:
1. Add a **fresh/throwaway account** first, to confirm login, match
   detection, and upload all work without touching your main account
2. Verify uploads land correctly on ballchasing (check visibility
   settings — defaults to `public` in `uploader.UploadReplay`, change
   the call in `internal/accounts/manager.go` if you want `unlisted`
   instead)
3. Only then add your main account

## Known gaps to fill in before this runs

- [ ] Real tray icon asset (currently text-only tooltip)
- [ ] Visual polish — current UI is functional (React + Vite, dark
      theme) but not designed
- [ ] `LICENSE` copyright line and `go.mod` module path still have
      placeholder values (`[your name here]` /
      `github.com/yourusername/...`) — left for you to fill in
      deliberately rather than guessed

## Version history

### 0.1.3
- Added an explicit `no-matches` event: `matches.PollOnce` now returns
  the match count alongside its error, so `Manager.pollAccount` can
  tell "poll succeeded, history was just empty" apart from "poll
  failed" or "found matches." Surfaced in the event log as `poll
  succeeded — no matches in history`. Found this gap from live manual
  QA — after adding a real account, `uploaded_matches` staying `{}`
  was ambiguous between a clean empty poll and a silent failure, and
  the only way to tell them apart was manually checking the pending-
  uploads cache and dev-server logs.
- `EventPayload` gained a `Manual` field, threaded through
  `pollAccount`/`handleMatch` and every event they emit
  (`no-matches`, `match-detected`, `upload-complete`, `upload-error`,
  `auth-error`, `cache-cleared`), so the frontend log can tell a
  scheduled poll's outcome apart from a manual "Poll Now" click —
  previously both produced an identical message.
- Fixed a related ordering bug in `AccountCard.tsx`: the "manual poll
  triggered" log line was appended *after* `await
  api.pollAccountNow(...)` resolved, but that call blocks on the Go
  side until the whole poll finishes — so the poll's own outcome
  event could reach the log before the line announcing the poll had
  even started. Moved the log call to before the await.

### 0.1.2
- Built the real frontend: React + TypeScript + Vite, scaffolded via
  `wails init -t react-ts` (harvested for build tooling only — our own
  `main.go`/`app.go`/`tray.go` were kept as-is, not overwritten).
  `frontend/src` now has `App.tsx`, a hand-typed `api.ts` wrapper
  around the bound Go methods, and one component per piece of UI
  (account card, three-step add-account wizard, two-step reauth
  dialog, settings bar, event log).
- Found and fixed two real bugs while wiring this up:
  `main.go` had `AssetServer: nil` — a placeholder from before the
  frontend existed — which crashed the app at startup
  (`AssetServer options invalid`); and the tray's "Show Window"
  callback was a no-op TODO, now calls `runtime.WindowShow`.
  Both were caught by actually running `wails dev`, not just
  `go build`.
- Verified end-to-end with `wails dev`: confirmed `GetPollIntervalSecs`
  / `GetHTTPTimeoutSecs` load real values from `config.json` on
  startup, `SetPollIntervalSecs` round-trips to disk, and the
  add-account flow's `SubmitAddAccountCode` correctly reaches Epic's
  live OAuth API — a deliberately-invalid code came back with Epic's
  real `authorization_code_not_found` error, proving the whole chain
  (React → Wails bridge → Go → `rlapi` → Epic's servers → error
  propagated back to the UI) works.
- `wails dev` bumped `wails` from v2.9.2 to v2.16.0 (matching the
  installed CLI) and Go to 1.25.0 in `go.mod`; removed a stale
  `go.mod` comment left over from before `rlapi` was pinned to a real
  version.
- Fixed a real gap found during manual QA: backing out of the reauth
  dialog after opening the login link (but before submitting a code)
  left the account stuck showing "authenticating" indefinitely, since
  nothing reverted the status `BeginReauth` had set. Added
  `Manager.CancelReauth` / `App.CancelReauth`, wired to the reauth
  dialog's Cancel button. Verified by temporarily seeding a fake
  `needs_reauth` account in `config.json` and confirming the status
  reverts correctly on cancel.

### 0.1.1
- Rewrote `internal/auth/epic.go`, `internal/matches/poller.go`, and
  the relevant parts of `internal/accounts/manager.go` against
  `dank/rlapi` v0.1.23's actual downloaded source. The types assumed
  in 0.1.0 (`rlapi.Client`, `rlapi.NewClient`, `client.LoginWithEpic`,
  a `.Matches` field on the history response) never existed — the
  real shape is `EGS` (Epic OAuth) → `PsyNet` (HTTP auth) →
  `PsyNetRPC` (the actual authenticated connection).
- This surfaced a real UX consequence: there's no automated browser
  callback for Epic login, so adding/reauthenticating an account is
  now a paste-a-code flow (see "Frontend contract"). `go.mod` is
  pinned to the real `v0.1.23` tag instead of a placeholder `v0.0.0`.
- Verified `internal/uploader/ballchasing.go` against ballchasing.gg's
  public API docs, `internal/secrets/secrets.go` against `go-keyring`
  v0.2.6's real source, and `tray.go` against `systray` v1.2.2's real
  source — all three were already correct, no changes needed.
- `go build ./...` and `go vet ./...` (whole module, including
  `main.go`) pass clean.
- Git/GitHub set up: private repo, repo-scoped commit identity (no
  real name/email in history), SSH key auth scoped to this repo only.

### 0.1.0
- Initial scaffold: system-tray + Wails app structure, multi-account
  design, ballchasing upload, OS-keyring credential storage, config
  persistence, poll-overlap and failed-upload-retry handling. The
  `dank/rlapi` integration was written against documentation/README
  guesses rather than verified source (see 0.1.1).
