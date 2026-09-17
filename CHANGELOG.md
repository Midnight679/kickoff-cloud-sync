# Changelog

## 0.2.3
- **Poll summary now always shows**, including "0 found, 0 uploaded", instead of disappearing when a poll finds nothing — gives positive confirmation a poll actually ran.
- **Update checker improvements**: now rechecks every 24 hours instead of just once at startup. Added "Dismiss" and "Skip this version" to the update banner, plus a "Check for updates" button in Settings that always reports the real state.
- Added an Acknowledgments section to the README crediting [Rockpload](https://github.com/LEX0RE/rockpload) as inspiration.

## 0.2.2
- **Version marker**: a subtle version label now shows in the bottom-right corner of the window, so you can tell at a glance which build you're running.
- **README**: Getting started now leads with the pre-built installer from Releases, with building from source as the alternative. Added a disclaimer about Chrome download warnings and Windows SmartScreen/antivirus false positives on the unsigned installer.

## 0.2.1
- **Last-poll summary**: each account card now shows how many replays were found and uploaded during its most recently completed poll, so you can tell what happened without switching to the settings/log view. Only ever reflects the latest cycle — not history — and disappears entirely when there's nothing to report.
- **Fixed a real "too many requests" error from ballchasing**: uploads and the background title-renaming step had no rate limiting or throttling at all, so a poll that found several matches at once could burst past ballchasing's documented limits (2 calls/second on their replay-detail endpoints). Every outbound call now goes through a shared rate limiter, with automatic retry and backoff if a 429 still gets through.

## 0.2.0
- **The repository is now public.**
- **Fixed a real bug**: replay downloads were failing with "expected an https URL" — Psyonix's own replay-serving endpoint uses plain HTTP, not HTTPS. The scheme check added during the earlier security review was too strict; it now accepts both, while still rejecting local/internal hosts (the actual protection that check exists for).
- **Update notice**: the app checks GitHub's releases once at startup and shows a small notice with a link if a newer version is available. It never downloads or installs anything automatically — you still run the new installer yourself.
- **Safer in-place upgrades**: the installer now closes any running instance before overwriting files (previously only the uninstaller did this), since the app hides to the tray rather than quitting and could otherwise block an upgrade from replacing the locked executable.

## 0.1.9
- **Per-account replay visibility**: each account now has its own public/unlisted/private dropdown, applied to every future upload from that account. Defaults to public; doesn't affect replays already uploaded.
- **Real Windows installer**, built and tested end-to-end via NSIS: installs per-user (no admin/UAC needed), creates working Start Menu and desktop shortcuts, and shows up correctly in Add/Remove Programs. Confirmed on a real install — including that toast notifications work correctly with Wails' stock shortcut, no extra customization needed.
- **Uninstall now offers to remove local data**: a prompt asks whether to also delete saved accounts, settings, and cached credentials; declining leaves everything in place for a future reinstall. The uninstaller also terminates a running instance first (the app hides to the tray rather than quitting) and cleans up the "launch at startup" registry entry if it was enabled.
- Fixed `THIRD_PARTY_LICENSES.md`: removed a section describing build-only tooling that was never actually shipped or a real dependency.

## 0.1.8
- **Custom app icon**: replaced the default Wails icon everywhere — the compiled `.exe`, taskbar, window title bar, and system tray — with a car-on-a-cloud design in Rocket League's default blue.
- **Tray error indicator**: the tray icon now shows a small red badge whenever any account needs reauthentication, mirroring the settings gear's error state so it's visible even while the window is hidden. Clears automatically once resolved.
- Fixed a `.gitignore` gap where the blanket `/build/` rule had silently kept the app's icon and Windows build config (`appicon.png`, `icon.ico`, `info.json`, `wails.exe.manifest`) out of version control since the repo was first set up. All four are now tracked; actual build output (`build/bin/`, generated binaries) stays ignored.

## 0.1.7
- **Security review**: ran `govulncheck` (Go) and `npm audit` against every dependency — zero known vulnerabilities in either. Manually reviewed the full codebase for common exploit classes and fixed three findings, all low-severity given this is a local single-user desktop app with no exposed network surface, but hardened anyway:
  - Match/account IDs are now validated (alphanumeric-only) before being used to construct any file path, closing a theoretical path-traversal gap in the pending-uploads cache.
  - Replay URLs are now validated (must be `https://`, can't point at `localhost`/an empty host) before being fetched, as defense in depth against ever blindly downloading from an unexpected address.
  - The poll interval can no longer be set below 10 minutes — previously unbounded, so `0` or a negative value could busy-loop and hammer Epic's and ballchasing's APIs. Enforced everywhere: the setter, the default config, and clamped on load for any existing config with a lower stored value.
- Added a note to the README disclosing that this project is AI-assisted / "vibe coded," in the interest of transparency.

## 0.1.6
- **Closing the window no longer quits the app** — clicking the close button now hides it to the tray instead, matching what a background uploader should actually do. Use the tray icon's Quit option to actually exit.
- **Launch at Windows startup**: a new checkbox in Settings, backed by the standard per-user registry Run key (no installer or admin rights needed). The registry is the live source of truth rather than a cached setting, so it can't drift out of sync if removed via Task Manager's Startup tab. An auto-launched instance starts hidden in the tray instead of popping up a window.
- **Replay auto-naming**: uploaded replays are no longer left with their default filename-based title. After upload, ballchasing's own authoritative parse of the replay (mode, teams, score) is used to set a real title — `"Ranked Doubles — Win"` for a normal match, or for a private match, the lobby's own custom team names if the host set any (e.g. `"Private — POLAR BEARS vs WHISKER GOBLINS"`), falling back to player rosters (`"Private — Alice, Bob vs Carol, Dave"`) if not, since win/loss is less meaningful there. Verified against real already-uploaded replays (including one with real custom team names) before rolling out.

## 0.1.5
- **Settings and logs moved to their own screen**, opened via a gear icon in the header. Poll interval and network timeout settings live there now instead of cluttering the main account list.
- **In-app server log capture**: a new `internal/logbuf` package captures everything written to Go's standard `log` package (network errors, retry failures, etc.) into a 1000-line ring buffer, viewable in-app as a "Server log" tab alongside the existing "Event log" — both now retain up to 1000 lines (up from 200).
- **Problem indicator**: the settings gear turns solid red and stays that way whenever any account needs reauthentication — persistent across restarts, not tied to any single event, and clears automatically once the account is fixed. Deliberately does *not* trigger on expected/self-resolving conditions like ballchasing's daily upload quota.
- Added a paired OS notification that fires once per account transitioning into "needs reauth" (both a live disconnect and a stale token discovered at app startup). The registry-side registration and process-level Windows API call are both in place, but full display hasn't been confirmed yet — Windows likely also requires a Start Menu shortcut with a matching AppUserModelID, which only exists for a properly installed build, not `wails dev`. To verify against our first real installer build.
- Added a disclaimer to the README: the app uses a reverse-engineered, unofficial Epic/Psyonix API (not endorsed by either), and launching the real Rocket League client while connected can log you out of your party/social session (harmless to your account/match standing).
- Completed a full dependency license audit (`go-licenses` + `license-checker`) — everything is permissive (MIT/Apache-2.0/BSD/ISC), no copyleft. Documented in the new `THIRD_PARTY_LICENSES.md`.

## 0.1.4
- **Silent reconnection**: the app now detects a dropped connection (e.g. Psyonix's `DuplicateLogin`, which happens routinely when the real Rocket League client logs into the same account) and silently reconnects using the stored refresh token, instead of requiring a full browser-based reauth after every gaming session.
- Paused accounts now get zero Epic API activity at app startup — they're connected lazily only the first time they're actually polled (manually, or after being resumed), so launching the app while a paused account's owner is mid-match can no longer trigger a login collision.
- Fixed a redundant-effort bug: a match stuck on a retryable upload failure (e.g. ballchasing's daily quota) was being re-downloaded and re-uploaded on every poll, on top of the dedicated retry pass already handling it.
- Fixed a real visibility gap: once an account's full match history was already uploaded, a successful poll produced no log output at all, indistinguishable from a silent failure. The log now reports "no new matches, N already uploaded" explicitly.
- The poll interval setting is now entered in minutes instead of seconds.
- Filled in the `LICENSE` copyright holder and the `go.mod` module path (previously left as placeholders).
- Renamed the app's internal storage (config folder and OS credential store identifier) and the visible window/tray title to match the project's actual name; existing account data was migrated over with no loss.
- Restructured the documentation: `README.md` is now a concise, user-facing guide; implementation details moved to `ARCHITECTURE.md`; version history moved to this file.

## 0.1.3
- Added a `no-matches` event so the log can distinguish "poll succeeded, nothing new" from a real failure.
- Poll events now carry whether they came from a manual "Poll Now" click or the scheduled cycle, so the log can tell them apart.
- Fixed a log-ordering bug where a manual poll's "triggered" message could appear after its own result.

## 0.1.2
- Built the real frontend: React + TypeScript + Vite, with a full account list, a three-step add-account wizard, a two-step reauth dialog, settings, and an event log.
- Fixed two startup bugs found while wiring up the frontend: a nil asset server that crashed the app on launch, and a no-op tray "Show Window" button.
- Fixed an account status that could get stuck on "authenticating" if a reauth was cancelled partway through.
- Verified end-to-end against the real Epic OAuth API and the real Wails runtime.

## 0.1.1
- Rewrote the Epic/Rocket League API integration against the real `dank/rlapi` library source — the originally scaffolded API shape didn't exist and needed a full rework (see [ARCHITECTURE.md](ARCHITECTURE.md) for the current design).
- Verified the ballchasing.com, credential storage, and system tray integrations against their real APIs/source.
- Set up git and a private GitHub repository.

## 0.1.0
- Initial scaffold: system tray + desktop app shell, multi-account design, ballchasing.com upload, secure credential storage, config persistence, and basic reliability handling (poll-overlap guard, failed-upload retry).
