package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
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
