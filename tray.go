package main

import (
	_ "embed"
	"sync"

	"github.com/energye/systray"
)

//go:embed build/windows/icon.ico
var trayIconBytes []byte

//go:embed build/windows/icon-error.ico
var trayIconErrorBytes []byte

// trayErrorState tracks whether the tray is currently showing the
// error-badged icon, so SetTrayErrorState only touches the native
// icon (which involves writing a temp file — see energye/systray)
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
func runTray(onShow func(), onPollNow func(), onQuit func()) {
	systray.Run(func() {
		systray.SetIcon(trayIconBytes)
		systray.SetTitle("Kickoff Cloud Sync")
		systray.SetTooltip("Kickoff Cloud Sync — Rocket League replay uploader")

		showItem := systray.AddMenuItem("Show Window", "Open the app window")
		pollItem := systray.AddMenuItem("Poll Now", "Check every account for new replays right now")
		systray.AddSeparator()
		quitItem := systray.AddMenuItem("Quit", "Quit the app")

		// Buffered, non-blocking sends: the callbacks run on this consumer
		// goroutine rather than directly on the native message-pump thread
		// that invokes them, so a slow poll or shutdown can't stall it.
		showCh := make(chan struct{}, 1)
		pollCh := make(chan struct{}, 1)
		quitCh := make(chan struct{}, 1)
		notify := func(ch chan struct{}) {
			select {
			case ch <- struct{}{}:
			default:
			}
		}

		showItem.Click(func() { notify(showCh) })
		pollItem.Click(func() { notify(pollCh) })
		quitItem.Click(func() { notify(quitCh) })
		// Double-clicking the tray icon does the same thing as "Show Window".
		systray.SetOnDClick(func(systray.IMenu) { notify(showCh) })

		go func() {
			for {
				select {
				case <-showCh:
					onShow()
				case <-pollCh:
					onPollNow()
				case <-quitCh:
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
