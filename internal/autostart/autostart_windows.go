//go:build windows

// Package autostart manages whether this app launches automatically
// when the user logs into Windows, via the standard per-user registry
// Run key. No installer, admin rights, or Start Menu shortcut needed
// — the registry is the single source of truth (queried live, not
// cached in config.json), so it can't drift out of sync with reality
// if the user removes the entry via Task Manager's Startup tab.
package autostart

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	valueName  = "KickoffCloudSync"
)

// IsEnabled reports whether the Run key entry currently exists.
func IsEnabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false, nil // key doesn't exist yet — not an error, just disabled
	}
	defer key.Close()

	if _, _, err := key.GetStringValue(valueName); err != nil {
		return false, nil
	}
	return true, nil
}

// SetEnabled adds or removes the Run key entry. When enabling, it
// points at the currently-running executable with a --hidden flag,
// so an auto-launched instance starts minimized to the tray instead
// of popping up a window on every login (see main.go).
func SetEnabled(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("opening registry run key: %w", err)
	}
	defer key.Close()

	if !enabled {
		if err := key.DeleteValue(valueName); err != nil && err != registry.ErrNotExist {
			return fmt.Errorf("removing startup entry: %w", err)
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating this executable: %w", err)
	}
	if err := key.SetStringValue(valueName, fmt.Sprintf(`"%s" --hidden`, exe)); err != nil {
		return fmt.Errorf("writing startup entry: %w", err)
	}
	return nil
}
