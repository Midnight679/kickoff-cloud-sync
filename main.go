package main

import (
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

func main() {
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

	// Tray runs in its own goroutine; it owns showing/hiding the
	// window and quitting the whole process.
	go runTray(
		func() { runtime.WindowShow(app.ctx) },
		func() {
			app.shutdown(app.ctx)
			runtime.Quit(app.ctx) // actually closes the window and ends the process
		},
	)

	err := wails.Run(&options.App{
		Title:  "Kickoff Cloud Sync",
		Width:  480,
		Height: 640,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
