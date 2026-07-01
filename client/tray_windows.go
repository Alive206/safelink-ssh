//go:build windows

package main

import (
	"os"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	systrayClassName       = "SystrayClass"
	systrayCallbackMessage = 0x0401
	wmLButtonUp            = 0x0202
	wmLButtonDblClk        = 0x0203
	gwlpWndProc            = ^uintptr(3)
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	procCallWindowProc     = user32.NewProc("CallWindowProcW")
	procSetWindowLongPtr   = user32.NewProc(setWindowLongPtrProcName())
	trayDoubleClickHookMu  sync.Mutex
	trayDoubleClickHook    trayWindowHook
	trayDoubleClickWndProc = windows.NewCallback(trayIconWndProc)
)

type trayWindowHook struct {
	hwnd       windows.HWND
	oldWndProc uintptr
	open       func()
}

func registerTrayIconDoubleClick(open func()) {
	hwnd := findSystrayWindow()
	if hwnd == 0 {
		return
	}

	trayDoubleClickHookMu.Lock()
	defer trayDoubleClickHookMu.Unlock()

	trayDoubleClickHook.open = open
	if trayDoubleClickHook.oldWndProc != 0 {
		return
	}

	oldWndProc, _, err := procSetWindowLongPtr.Call(
		uintptr(hwnd),
		gwlpWndProc,
		trayDoubleClickWndProc,
	)
	if oldWndProc == 0 && err != syscall.Errno(0) {
		return
	}

	trayDoubleClickHook.hwnd = hwnd
	trayDoubleClickHook.oldWndProc = oldWndProc
}

func trayIconWndProc(hwnd windows.HWND, message uint32, wParam, lParam uintptr) uintptr {
	if message == systrayCallbackMessage {
		switch lParam {
		case wmLButtonDblClk:
			if open := currentTrayOpenCallback(); open != nil {
				go open()
			}
			return 0
		case wmLButtonUp:
			return 0
		}
	}

	trayDoubleClickHookMu.Lock()
	oldWndProc := trayDoubleClickHook.oldWndProc
	trayDoubleClickHookMu.Unlock()
	if oldWndProc == 0 {
		return 0
	}

	result, _, _ := procCallWindowProc.Call(
		oldWndProc,
		uintptr(hwnd),
		uintptr(message),
		wParam,
		lParam,
	)
	return result
}

func currentTrayOpenCallback() func() {
	trayDoubleClickHookMu.Lock()
	defer trayDoubleClickHookMu.Unlock()
	return trayDoubleClickHook.open
}

func findSystrayWindow() windows.HWND {
	currentPID := uint32(os.Getpid())
	var found windows.HWND

	enumCallback := windows.NewCallback(func(hwnd windows.HWND, lParam uintptr) uintptr {
		var pid uint32
		windows.GetWindowThreadProcessId(hwnd, &pid)
		if pid != currentPID {
			return 1
		}
		if windowClassName(hwnd) != systrayClassName {
			return 1
		}
		found = hwnd
		return 0
	})

	_ = windows.EnumWindows(enumCallback, nil)
	return found
}

func windowClassName(hwnd windows.HWND) string {
	buf := make([]uint16, 256)
	n, err := windows.GetClassName(hwnd, &buf[0], int32(len(buf)))
	if err != nil || n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func setWindowLongPtrProcName() string {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		return "SetWindowLongPtrW"
	}
	return "SetWindowLongW"
}
