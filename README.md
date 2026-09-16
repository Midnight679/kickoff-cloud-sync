# Kickoff Cloud Sync

A system-tray app that watches your recent Rocket League matches across multiple accounts and automatically uploads replays to [ballchasing.gg](https://ballchasing.com).

## Features

- **Multi-account** — track as many Rocket League accounts as you want, each with its own ballchasing.com token
- **No local Rocket League install required** — match history and replays are pulled directly from Epic's cloud API
- **Automatic deduplication** — never uploads the same match twice
- **Background polling** — checks for new matches on a configurable interval, with a manual "Poll Now" option per account
- **Secure credential storage** — tokens live in your OS's native credential store, never in plaintext
- **Resilient uploads** — a failed upload (network hiccup, ballchasing's daily quota, etc.) is cached and retried automatically; nothing is lost

## Requirements

- [Go](https://go.dev) 1.25+
- [Node.js](https://nodejs.org) 18+
- [Wails CLI](https://wails.io) v2
- An Epic Games account (Steam-linked accounts work too — see [Adding an account](#adding-an-account))
- A free [ballchasing.com](https://ballchasing.com) API token per account

## Getting started

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
git clone <this repo>
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

Uploads default to `public` on ballchasing.com. A UI toggle for unlisted/private is planned; for now, change the visibility argument in `internal/accounts/manager.go` if you want something else.

## How it works

The app polls Epic's match history API for each connected account, downloads any new replay directly from Epic's cloud, and uploads it to ballchasing.com. See [ARCHITECTURE.md](ARCHITECTURE.md) for implementation details.

## Known limitations

- Epic's match history API only exposes a recent-matches window (the same one shown in-game — the most recent 20), so very old matches won't be found. The default poll interval comfortably covers this.
- ballchasing.com's free API tier caps uploads at 10/day — anything beyond that is queued and retried automatically once the quota resets
- No custom tray icon yet (text-only tooltip)
- Replay visibility isn't yet configurable from the UI

## License

MIT — see [LICENSE](LICENSE).

## Learn more

- [ARCHITECTURE.md](ARCHITECTURE.md) — internal design, event system, reliability details
- [CHANGELOG.md](CHANGELOG.md) — version history
