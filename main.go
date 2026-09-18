package main

import (
	"context"
	"embed"
	"io"
	"log"
	"os"

	"git.sr.ht/~jackmordaunt/go-toast/v2"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Midnight679/kickoff-cloud-sync/internal/logbuf"
)

//go:embed all:frontend/dist
var assets embed.FS

// notifyAUMID is this app's Application User Model ID — the identity
// Windows uses to route and display toast notifications (see
// sendReauthNotification in app.go). No spaces, matching Windows'
// AUMID convention. Must stay stable across releases: it's written
// into the registry (toast.SetAppData) and must also match whatever
// AUMID any Start Menu shortcut is given when the app is installed.
const notifyAUMID = "Midnight679.KickoffCloudSync"

// notifyGUID identifies this app's toast activation handler to the
// Windows Runtime — fixed and arbitrary, just needs to stay stable
// across releases.
const notifyGUID = "{6f1b1e2a-6c3d-4f7a-9d2e-2b7a5c8f1a3b}"

// singleInstanceID names the lock Wails uses to detect an already
// running copy of this app (see SingleInstanceLock below). Fixed and
// arbitrary; it only has to be unique to this app and stay stable
// across releases.
const singleInstanceID = "b7f3c1d2-5e4a-4c8b-9f6d-kickoff-cloud-sync"

func main() {
	// Handled before anything else — no GUI, tray, or logbuf setup —
	// so the uninstaller's `--purge` invocation runs to completion and
	// exits cleanly on its own. See purge.go.
	for _, arg := range os.Args[1:] {
		if arg == "--purge" {
			purgeAllData()
			return
		}
	}

	// Capture everything the standard log package writes (from every
	// package, not just this one) into an in-memory ring buffer the
	// frontend can read — see internal/logbuf. Still writes to real
	// stderr too, for `wails dev`'s console.
	log.SetOutput(io.MultiWriter(os.Stderr, logbuf.Writer()))

	// Both calls are required for toast notifications to actually
	// display on an unpackaged Windows app — see aumid_windows.go for
	// why SetAppData alone isn't enough.
	setProcessAppUserModelID(notifyAUMID)
	if err := toast.SetAppData(toast.AppData{AppID: notifyAUMID, GUID: notifyGUID}); err != nil {
		log.Printf("failed to register app data for notifications: %v", err)
	}

	app := NewApp()

	// Passed by the registry Run-key entry (see internal/autostart)
	// when the app launches automatically at login, so it starts
	// quietly in the tray instead of popping up a window every time
	// Windows starts.
	startHidden := false
	for _, arg := range os.Args[1:] {
		if arg == "--hidden" {
			startHidden = true
		}
	}

	err := wails.Run(&options.App{
		// Overwritten almost immediately after startup (see
		// updateWindowTitle in app.go), which appends the running
		// upload count — this is just what briefly shows before that
		// first call lands.
		Title:       appTitle,
		Width:       480,
		Height:      640,
		// Width is fixed (Min/MaxWidth pinned to the same value) since
		// the account list is a single column with nothing to gain from
		// extra horizontal space; height stays freely resizable so
		// someone with several accounts can make the window taller to
		// see more cards without scrolling.
		MinWidth:    480,
		MaxWidth:    480,
		StartHidden: startHidden,
		// Clicking the window's close button hides it instead of
		// quitting the whole app — this is a background uploader, so
		// the tray icon (Quit menu item) is the actual way to exit.
		HideWindowOnClose: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// Without this, starting the app while it is already running
		// (easy to do: it lives hidden in the tray, and launch-at-login
		// starts it with no window at all) gives a second full instance.
		// The two then log into the same Epic accounts and kick each
		// other's PsyNet session every poll cycle, upload the same
		// replays, and overwrite each other's config.json. Wails ends
		// the second process inside wails.Run and calls
		// OnSecondInstanceLaunch in the first one instead.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: singleInstanceID,
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				for _, arg := range data.Args {
					if arg == "--hidden" {
						return // a second auto-start should stay quiet
					}
				}
				runtime.WindowUnminimise(app.ctx)
				runtime.WindowShow(app.ctx)
			},
		},
		OnStartup: func(ctx context.Context) {
			// Started here rather than before wails.Run so that only
			// the instance holding the single-instance lock ever gets a
			// tray icon. It also means app.ctx is always set before a
			// tray click can use it. Tray runs in its own goroutine; it
			// owns showing/hiding the window and quitting the process.
			go runTray(
				func() { runtime.WindowShow(ctx) },
				func() {
					app.shutdown(ctx)
					runtime.Quit(ctx) // actually closes the window and ends the process
				},
			)
			app.startup(ctx)
		},
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
