# Changelog

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
