//go:build !windows

package main

// setProcessAppUserModelID is a Windows-specific concept (see
// aumid_windows.go) — a no-op everywhere else.
func setProcessAppUserModelID(aumid string) {}
