# Third-party licenses

This project (MIT-licensed, see [LICENSE](LICENSE)) depends on the packages below. All of them use permissive licenses (MIT, Apache-2.0, BSD-2/3-Clause, ISC) — none are copyleft (GPL/LGPL/AGPL), so there's no license-compatibility issue with distributing this project publicly.

This list covers only what's actually compiled or bundled into the shipped app. Build-only tooling — the Wails CLI, Vite, TypeScript, and the rest of `frontend`'s `devDependencies` — isn't included, since none of it ships with the app or is a dependency of it.

Generated with [`google/go-licenses`](https://github.com/google/go-licenses) and [`license-checker`](https://www.npmjs.com/package/license-checker):

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

*Reflects the dependency set actually linked into the Windows build, not the full `go.mod` list — some indirect entries are pulled in only for other platforms and never compiled here.*

## npm (frontend)

| Package | License |
|---|---|
| [react](https://github.com/facebook/react) | MIT |
| [react-dom](https://github.com/facebook/react) | MIT |
| scheduler | MIT |

## A note on `dank/rlapi`

`rlapi`'s MIT license covers the *copyright* terms for using its code — that's separate from whether reverse-engineering and calling Epic/Psyonix's internal API complies with *their* terms of service. See the [Disclaimer](README.md#disclaimer) in the README for that distinction.
