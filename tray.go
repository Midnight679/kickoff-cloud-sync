package main

import (
	_ "embed"
	"sync"

	"github.com/getlantern/systray"
)

//go:embed build/windows/icon.ico
var trayIconBytes []byte

//go:embed build/windows/icon-error.ico
var trayIconErrorBytes []byte

// trayErrorState tracks whether the tray is currently showing the
// error-badged icon, so SetTrayErrorState only touches the native
// icon (which involves writing a temp file — see getlantern/systray)
// when the state actually changes.
var (
	trayErrorMu    sync.Mutex
	trayErrorState bool
)

// SetTrayErrorState swaps the tray icon to the error-badged variant
// (or back) to mirror the same "needs attention" condition shown by
// the settings gear in the frontend. Safe to call frequently — it
// only calls into systray when the state actually flips.
func SetTrayErrorState(hasError bool) {
	trayErrorMu.Lock()
	defer trayErrorMu.Unlock()
	if hasError == trayErrorState {
		return
	}
	trayErrorState = hasError
	if hasError {
		systray.SetIcon(trayIconErrorBytes)
	} else {
		systray.SetIcon(trayIconBytes)
	}
}

// runTray blocks, so it must be started in its own goroutine
// before wails.Run() (which also blocks) is called in main.go.
func runTray(onShow func(), onQuit func()) {
	systray.Run(func() {
		systray.SetIcon(trayIconBytes)
		systray.SetTitle("Kickoff Cloud Sync")
		systray.SetTooltip("Kickoff Cloud Sync — Rocket League replay uploader")

		showItem := systray.AddMenuItem("Show Window", "Open the app window")
		systray.AddSeparator()
		quitItem := systray.AddMenuItem("Quit", "Quit the app")

		go func() {
			for {
				select {
				case <-showItem.ClickedCh:
					onShow()
				case <-quitItem.ClickedCh:
					onQuit()
					systray.Quit()
					return
				}
			}
		}()
	}, func() {
		// systray exit cleanup, if needed
	})
}
