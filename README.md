# RL Replay Uploader

A system-tray app that watches recent Rocket League matches across
multiple accounts and auto-uploads replays to
[ballchasing.gg](https://ballchasing.com).

**Status: scaffold / work in progress.** This is a starting structure,
not a finished, tested app — several pieces are marked `TODO` and need
verifying against current library APIs before this will actually build
and run.

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
via `rlapi-py` (same underlying API), whose docs describe "match
history with replay URLs." There is intentionally no fallback to a
local `Demos` folder, so this can run on any machine with network
access, including one that's never had Rocket League installed.

Two caveats worth keeping in mind:
- The exact field names on rlapi's `MatchEntry` (replay URL) and
  `GetProfile` response (display name) aren't confirmed yet — check
  the current godoc once you have network access. Both are marked
  `TODO` in `internal/matches/poller.go` and `internal/auth/epic.go`.
- The cloud copy is almost certainly retention-limited to recent
  matches (same window as the in-game "Recent Matches" list — the
  most recent 20). At the default 5-minute poll interval this has a
  huge safety margin.

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
(without exposing the token itself). Actions: `ReauthAccount`,
`PauseAccount`/`ResumeAccount`, `RemoveAccount`,
`SetAccountBallchasingToken`, `SetFriendlyName`, `PollAccountNow`.
Global: `GetPollIntervalSecs`/`SetPollIntervalSecs`.

**Adding an account is a two-step flow, not one call:**
1. `BeginAddAccount()` — opens the Epic browser login. Blocks until
   the user finishes (or abandons) it, then returns
   `{pending_id, epic_display_name}`. Nothing is persisted yet.
2. `ConfirmAddAccount(pendingID, ballchasingToken, friendlyName)` —
   validates the token against ballchasing's own API (a real ping,
   not just a non-empty check) before saving anything. On failure,
   the pending login from step 1 is preserved server-side, so the
   frontend can just let the user retry with a different token
   rather than going through the browser login again.
3. `CancelAddAccount(pendingID)` — discards an in-progress flow if
   the user backs out of the token step.

Friendly names show in parentheses next to the Epic display name —
`DisplayName (FriendlyName)` — computed client-side; the backend
returns both fields separately.

Events (`runtime.EventsEmit`, payload is `accounts.EventPayload` with
an `account_id` field so the frontend can route to the right box):
`accounts-changed`, `match-detected`, `upload-complete`,
`upload-error`, `auth-error`. The bare-bones `frontend/index.html` in
this scaffold demonstrates wiring all of this up — treat it as a
reference for the bound method names and event payloads, not as the
UI itself.

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

This needs real internet access to resolve `dank/rlapi`, Wails, and
`systray` versions — the `go.mod` in this scaffold has placeholder
version pins.

### 2. Wails frontend tooling

This scaffold ships a bare-bones `frontend/index.html` so the project
compiles and the account-management flow is demonstrable. For the
full Wails dev experience (hot reload, build pipeline), install the
Wails CLI and consider running `wails init` in a separate folder to
compare scaffolds:

```
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails dev
```

### 3. Auth

`internal/auth/epic.go` opens rlapi's EGS browser login for each
account you add. Pick whichever identity provider matches how you
normally sign in (Epic account, or "Log in with Steam") on that
screen — no SDK downloads, no DLLs.

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

- [ ] Confirm `dank/rlapi`'s actual public method names against its
      current godoc (`NewClient`, `LoginWithEpic`, `RefreshToken`,
      `GetMatchHistory`, `GetProfile`, and their field names are
      best-guesses based on the README, not verified against
      compiled source)
- [ ] Real tray icon asset (currently text-only tooltip)
- [ ] Window show/hide wiring in `main.go`'s tray callback
- [ ] Frontend is a functional reference, not a designed UI — that's
      the part you're taking on
