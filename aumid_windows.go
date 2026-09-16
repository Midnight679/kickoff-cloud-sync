//go:build windows

package main

import (
	"log"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setProcessAppUserModelID associates this running process with the
// given AppUserModelID (AUMID), via the Win32
// SetCurrentProcessExplicitAppUserModelID API.
//
// This is a separate requirement from toast.SetAppData (see app.go):
// that call only writes the registry side of the registration
// (HKCU\Software\Classes\AppUserModelId\<AUMID>). Windows silently
// drops toast notifications from an unpackaged app unless the
// *running process itself* is also explicitly associated with that
// same AUMID — go-toast doesn't do this for you. Must be called once,
// before any UI is shown or any toast is pushed.
//
// Known limitation: even with this in place, Windows also expects a
// Start Menu shortcut for the app whose own AppUserModelID property
// matches this AUMID. That shortcut only exists for a properly
// installed build (e.g. via the NSIS installer `wails build`
// generates) — not for `wails dev`'s temporary executable. So this
// fix can't be fully verified until testing against an installed
// build.
func setProcessAppUserModelID(aumid string) {
	ptr, err := syscall.UTF16PtrFromString(aumid)
	if err != nil {
		log.Printf("failed to encode AppUserModelID %q: %v", aumid, err)
		return
	}
	proc := windows.NewLazySystemDLL("shell32.dll").NewProc("SetCurrentProcessExplicitAppUserModelID")
	if ret, _, _ := proc.Call(uintptr(unsafe.Pointer(ptr))); ret != 0 {
		log.Printf("SetCurrentProcessExplicitAppUserModelID(%q) failed: hresult=0x%x", aumid, ret)
	}
}
