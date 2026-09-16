package main

import (
	"github.com/getlantern/systray"
)

// runTray blocks, so it must be started in its own goroutine
// before wails.Run() (which also blocks) is called in main.go.
func runTray(onShow func(), onQuit func()) {
	systray.Run(func() {
		systray.SetTitle("Kickoff Cloud Sync")
		systray.SetTooltip("Kickoff Cloud Sync — Rocket League replay uploader")
		// TODO: replace with a real .ico/.png asset — see
		// frontend or an /assets folder for the icon bytes.

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
