//go:build !windows

package autostart

import "fmt"

// IsEnabled always reports disabled on non-Windows platforms — this
// feature currently only has a Windows implementation.
func IsEnabled() (bool, error) { return false, nil }

// SetEnabled fails on non-Windows platforms.
func SetEnabled(enabled bool) error {
	return fmt.Errorf("launch at login is only supported on Windows right now")
}
