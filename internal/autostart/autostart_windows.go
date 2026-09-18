//go:build windows

// Package autostart manages whether this app launches automatically
// when the user logs into Windows, via the standard per-user registry
// Run key. No installer, admin rights, or Start Menu shortcut needed
// — the registry is the single source of truth (queried live, not
// cached in config.json).
//
// IsEnabled also checks StartupApproved\Run: toggling an entry off via
// Task Manager's Startup tab does NOT remove it from the Run key (a
// check of the Run key alone would assume it did) — Task Manager
// leaves Run untouched and records the disabled state elsewhere.
package autostart

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath          = `Software\Microsoft\Windows\CurrentVersion\Run`
	startupApprovedPath = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`
	valueName           = "KickoffCloudSync"
)

// IsEnabled reports whether this app will actually launch at Windows
// login: the Run key entry must exist, and the user must not have
// disabled it via Task Manager's Startup tab since.
func IsEnabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false, nil // key doesn't exist yet — not an error, just disabled
	}
	defer key.Close()

	if _, _, err := key.GetStringValue(valueName); err != nil {
		return false, nil
	}
	return !disabledViaTaskManager(), nil
}

// disabledViaTaskManager reports whether the user turned this entry
// off via Task Manager's Startup tab. Doing so writes an 11-byte
// REG_BINARY value under StartupApproved\Run whose first byte is a
// state flag: 02 or 06 means enabled, 03 or 07 means disabled
// (undocumented by Microsoft, but well-established from
// reverse-engineering StartupApproved's behavior). No value present
// (or the key not existing at all) means "never touched in Task
// Manager" — i.e. not disabled that way.
func disabledViaTaskManager() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, startupApprovedPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()

	data, _, err := key.GetBinaryValue(valueName)
	if err != nil || len(data) == 0 {
		return false
	}
	switch data[0] {
	case 0x03, 0x07:
		return true
	default:
		return false
	}
}

// SetEnabled adds or removes the Run key entry. When enabling, it
// points at the currently-running executable with a --hidden flag,
// so an auto-launched instance starts minimized to the tray instead
// of popping up a window on every login (see main.go). It also clears
// a StartupApproved\Run disable left by Task Manager's Startup tab, if
// any — otherwise re-enabling here would write the Run key
// successfully (no error) while IsEnabled kept reporting false right
// after, since it checks that value too.
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

	clearTaskManagerDisable()
	return nil
}

// clearTaskManagerDisable flips an existing StartupApproved\Run
// disable flag (see disabledViaTaskManager) back to enabled, touching
// only the first byte — the rest of the value is opaque,
// Explorer-managed data not worth risking a malformed rewrite of for
// bytes whose meaning isn't documented. Best-effort and silent: if the
// key or value doesn't exist at all, there's nothing to clear (an
// absent value already reads as "not disabled" per
// disabledViaTaskManager), and any failure here shouldn't undo the
// Run key write SetEnabled already completed successfully.
func clearTaskManagerDisable() {
	key, err := registry.OpenKey(registry.CURRENT_USER, startupApprovedPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer key.Close()

	data, _, err := key.GetBinaryValue(valueName)
	if err != nil || len(data) == 0 {
		return
	}
	switch data[0] {
	case 0x03:
		data[0] = 0x02
	case 0x07:
		data[0] = 0x06
	default:
		return // already enabled, or a flag value we shouldn't guess at
	}
	_ = key.SetBinaryValue(valueName, data)
}
