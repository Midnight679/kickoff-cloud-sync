# Kickoff Cloud Sync

A system-tray app that watches your recent Rocket League matches across multiple accounts and automatically uploads replays to [ballchasing.gg](https://ballchasing.com).

## Disclaimer

This app talks to Rocket League's match-history API the same way the game client itself does, using [dank/rlapi](https://github.com/dank/rlapi). As that project states, this is a **reverse-engineered, unofficial integration** — it is not endorsed, sponsored, or approved by Epic Games or Psyonix, and isn't covered by any official API agreement. Use it at your own risk.

One practical consequence: Epic/Psyonix only allow one active session per account on this connection. **Launching the real Rocket League client while this app is connected to the same account can log you out of your party/social session** (a `DuplicateLogin` conflict) — whichever side connects most recently wins, and the app's background reconnect can trigger this if it happens to fire while you're actively playing. It doesn't affect your actual match or account standing, just the social/party layer. A longer poll interval (see [Settings](#settings)) reduces how often this can happen, though it can't eliminate it entirely — see [ARCHITECTURE.md](ARCHITECTURE.md#silent-reconnection) for the full explanation.

This project is also AI-assisted / "vibe coded" — built in collaboration with Claude, with a human reviewing, testing, and directing every change rather than writing all of it by hand. Disclosed here in the interest of transparency, not as a caveat on quality.

The [installer releases](https://github.com/Midnight679/kickoff-cloud-sync/releases) aren't code-signed. Chrome may flag the download, and Windows SmartScreen or your antivirus may flag the installer itself — this is a well-documented false-positive pattern (`Trojan:*/Wacatac.*!ml`-style heuristic detections) that affects unsigned Go/NSIS-built applications broadly, not something specific to this app. If you hit it and want to proceed anyway: "Keep" on Chrome's download warning, then "More info" → "Run anyway" on SmartScreen's.

## Features

- **Multi-account** — track as many Rocket League accounts as you want, each with its own ballchasing.com token
- **No local Rocket League install required** — match history and replays are pulled directly from Epic's cloud API
- **Automatic deduplication** — never uploads the same match twice
- **Background polling** — checks for new matches on a configurable interval, with a manual "Poll Now" option per account
- **Secure credential storage** — tokens live in your OS's native credential store, never in plaintext
- **Resilient uploads** — a failed upload (network hiccup, ballchasing's daily quota, etc.) is cached and retried automatically; nothing is lost
- **Update notices** — checks once a day for a newer release and shows a dismissible banner with a link; nothing downloads or installs automatically. "Skip this version" silences it for good; "Check for updates" in Settings checks on demand

## Requirements

- An Epic Games account (Steam-linked accounts work too — see [Adding an account](#adding-an-account))
- A free [ballchasing.com](https://ballchasing.com) API token per account

Building from source instead of using the installer additionally requires:

- [Go](https://go.dev) 1.26+
- [Node.js](https://nodejs.org) 18+
- [Wails CLI](https://wails.io) v2

## Getting started

Most people should just grab the latest installer from [Releases](https://github.com/Midnight679/kickoff-cloud-sync/releases) — download `kickoff-cloud-sync-vX.Y.Z-installer.exe`, run it, done. See the [Disclaimer](#disclaimer) above regarding SmartScreen/antivirus warnings on the unsigned installer.

To build from source instead (for development or if you'd rather not run a pre-built binary):

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
git clone https://github.com/Midnight679/kickoff-cloud-sync.git
cd kickoff-cloud-sync
wails dev
```

`wails dev` installs frontend dependencies, starts the app, and opens both a native window and a browser-accessible mirror at `http://localhost:34115`.

To build a standalone binary:

```bash
wails build
```

## Usage

### Adding an account

Click **+ Add Account** and follow the prompts:

1. **Open Epic Login** — opens Epic's sign-in page in your browser. Choosing "Log in with Steam" (or Xbox/PlayStation/Nintendo) works for Steam-linked accounts too, since it's the same underlying Epic identity.
2. **Paste the authorization code** — after logging in, Epic shows a small JSON page; copy the `authorizationCode` value and paste it back into the app.
3. **Set a ballchasing token and friendly name** — grab your token from your [ballchasing.com account settings](https://ballchasing.com/doc/api).

Reauthenticating an existing account (if its login expires) follows the same open-link-then-paste-code pattern.

### Settings

- **Poll interval** — how often, in minutes, the app checks for new matches
- **Network timeout** — how long to wait on downloads/uploads before giving up

### Replay visibility

Each account has its own **Replay visibility** dropdown (public/unlisted/private) in its account card, applied to every future upload from that account. Defaults to `public`. Changing it doesn't affect replays already uploaded.

## How it works

The app polls Epic's match history API for each connected account, downloads any new replay directly from Epic's cloud, and uploads it to ballchasing.com. See [ARCHITECTURE.md](ARCHITECTURE.md) for implementation details.

## Known limitations

- See [Disclaimer](#disclaimer) above regarding the reverse-engineered API and the possibility of getting logged out of your party/social session while playing.
- Epic's match history API only exposes a recent-matches window (the same one shown in-game — the most recent 20), so very old matches won't be found. The default poll interval comfortably covers this.
- ballchasing.com's free API tier caps uploads at 10/day — anything beyond that is queued and retried automatically once the quota resets

## License

MIT — see [LICENSE](LICENSE). All dependencies use permissive licenses (MIT/Apache-2.0/BSD/ISC) with no copyleft — see [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) for the full audit.

## Acknowledgments

Inspired by [Rockpload](https://github.com/LEX0RE/rockpload), a similar Rocket League replay auto-uploader. Building a separate version from scratch was the more appealing path, for the sake of having full creative and functional control over how every feature works.

## Learn more

- [ARCHITECTURE.md](ARCHITECTURE.md) — internal design, event system, reliability details
- [CHANGELOG.md](CHANGELOG.md) — version history
- [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) — dependency license audit
