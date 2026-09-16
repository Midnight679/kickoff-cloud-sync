# Third-party licenses

This project (MIT-licensed, see [LICENSE](LICENSE)) depends on the packages below. All of them use permissive licenses (MIT, Apache-2.0, BSD-2/3-Clause, ISC) — none are copyleft (GPL/LGPL/AGPL), so there are no license-compatibility issues with distributing this project, including publicly.

Generated with [`google/go-licenses`](https://github.com/google/go-licenses) (Go, resolved against the actual Windows build) and [`license-checker`](https://www.npmjs.com/package/license-checker) (npm). Re-run these yourself after adding or updating a dependency:

```bash
go-licenses csv .
cd frontend && npx license-checker --summary
```

## Go (backend)

| Package | License |
|---|---|
| [github.com/dank/rlapi](https://github.com/dank/rlapi) | MIT |
| [github.com/wailsapp/wails/v2](https://github.com/wailsapp/wails) | MIT |
| [github.com/getlantern/systray](https://github.com/getlantern/systray) | Apache-2.0 |
| [github.com/zalando/go-keyring](https://github.com/zalando/go-keyring) | MIT |
| github.com/danieljoos/wincred | MIT |
| github.com/getlantern/context | Apache-2.0 |
| github.com/getlantern/errors | Apache-2.0 |
| github.com/getlantern/golog | Apache-2.0 |
| github.com/getlantern/hex | BSD-3-Clause |
| github.com/getlantern/hidden | Apache-2.0 |
| github.com/getlantern/ops | Apache-2.0 |
| github.com/go-stack/stack | MIT |
| github.com/gorilla/websocket | BSD-2-Clause |
| github.com/leaanthony/go-ansi-parser | MIT |
| github.com/leaanthony/slicer | MIT |
| github.com/leaanthony/u | MIT |
| github.com/oxtoacart/bpool | Apache-2.0 |
| github.com/pkg/errors | BSD-2-Clause |
| github.com/rivo/uniseg | MIT |
| github.com/wailsapp/go-webview2 | MIT (combridge) / ISC (webviewloader) |
| golang.org/x/sys | BSD-3-Clause |

*(This is the dependency set actually linked into the Windows build, per `go-licenses`, not the full `go.mod` list — some indirect entries in `go.mod` are only pulled in for other platforms and never actually compiled in.)*

## npm (frontend, production)

Only what's actually bundled into `frontend/dist` — build-time-only tooling (Vite, TypeScript, Babel, etc.) is excluded since none of it ships in the built app.

| Package | License |
|---|---|
| [react](https://github.com/facebook/react) | MIT |
| [react-dom](https://github.com/facebook/react) | MIT |
| scheduler | MIT |

Build tooling (`devDependencies`) is all MIT/ISC/Apache-2.0 as well, with one exception: `caniuse-lite` (a transitive dependency of Vite's build pipeline, used only to decide browser-compatibility targets at build time) is `CC-BY-4.0`. Its data is never bundled into the shipped output, so no attribution obligation applies — but it's noted here for completeness.

## A note on `dank/rlapi` specifically

`rlapi`'s MIT license covers the *copyright* terms for using its code — that's separate from the question of whether reverse-engineering and calling Epic/Psyonix's internal API is allowed under *their* terms of service. See the [Disclaimer](README.md#disclaimer) in the README for that distinction.
