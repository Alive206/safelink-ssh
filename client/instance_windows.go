//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const swRestore = 9

var (
	instanceUser32          = syscall.NewLazyDLL("user32.dll")
	procFindWindowW         = instanceUser32.NewProc("FindWindowW")
	procShowWindow          = instanceUser32.NewProc("ShowWindow")
	procSetForegroundWindow = instanceUser32.NewProc("SetForegroundWindow")
)

// acquireSingleInstance returns false after restoring an already-running main window.
func acquireSingleInstance() (func(), bool) {
	name, _ := windows.UTF16PtrFromString("Local\\SafeLink.MainInstance")
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return func() {}, true
	}
	if err == windows.ERROR_ALREADY_EXISTS {
		restoreExistingMainWindow()
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return func() {}, false
	}
	return func() { _ = windows.CloseHandle(handle) }, true
}

func restoreExistingMainWindow() {
	title, _ := windows.UTF16PtrFromString("SafeLink")
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd != 0 {
		procShowWindow.Call(hwnd, swRestore)
		procSetForegroundWindow.Call(hwnd)
	}
}
