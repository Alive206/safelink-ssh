//go:build windows && installer

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

const (
	whiteBrush = 0

	wsOverlapped  = 0x00000000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsChild       = 0x40000000
	wsVisible     = 0x10000000
	wsBorder      = 0x00800000
	wsTabStop     = 0x00010000

	ssLeft           = 0x00000000
	ssEtchedHorz     = 0x00000010
	esAutoHScroll    = 0x00000080
	bsPushButton     = 0x00000000
	bsDefPushButton  = 0x00000001
	bsAutoCheckBox   = 0x00000003
	bstChecked       = 1
	bstUnchecked     = 0
	defaultGUIFont   = 17
	fwRegular        = 400
	fwSemiBold       = 600
	defaultCharset   = 1
	clearTypeQuality = 5
	idcArrow         = 32512
	idInstallDirEdit = 1001
	idBrowseButton   = 1002
	idDesktopCheck   = 1003
	idInstallButton  = 1005
	idCancelButton   = 1006

	wmCreate         = 0x0001
	wmCommand        = 0x0111
	wmClose          = 0x0010
	wmDestroy        = 0x0002
	wmSetFont        = 0x0030
	wmGetText        = 0x000D
	wmGetTextLength  = 0x000E
	wmCtlColorBtn    = 0x0135
	wmCtlColorStatic = 0x0138
	bmGetCheck       = 0x00F0
	bmSetCheck       = 0x00F1

	transparent = 1

	swShow = 5

	smCXScreen = 0
	smCYScreen = 1

	maxPath = 260

	csidlDesktopDirectory = 0x0010

	bifReturnOnlyFSDirs = 0x0001
	bifEditBox          = 0x0010
	bifNewDialogStyle   = 0x0040
	bifUseNewUI         = bifEditBox | bifNewDialogStyle
	bffmInitialized     = 1
	bffmSetSelectionW   = 0x0400 + 103
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type point struct {
	x int32
	y int32
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type browseInfo struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

type installDialogState struct {
	defaults     installOptions
	result       installOptions
	accepted     bool
	done         bool
	hwnd         uintptr
	installEdit  uintptr
	desktopCheck uintptr
	bodyFont     uintptr
	titleFont    uintptr
	headingFont  uintptr
	captionFont  uintptr
}

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")

	procRegisterClassExW  = user32.NewProc("RegisterClassExW")
	procCreateWindowExW   = user32.NewProc("CreateWindowExW")
	procDefWindowProcW    = user32.NewProc("DefWindowProcW")
	procDestroyWindow     = user32.NewProc("DestroyWindow")
	procDispatchMessageW  = user32.NewProc("DispatchMessageW")
	procGetDlgItem        = user32.NewProc("GetDlgItem")
	procGetMessageW       = user32.NewProc("GetMessageW")
	procCreateFontW       = gdi32.NewProc("CreateFontW")
	procGetStockObject    = gdi32.NewProc("GetStockObject")
	procSetBkMode         = gdi32.NewProc("SetBkMode")
	procGetSystemMetrics  = user32.NewProc("GetSystemMetrics")
	procGetWindowTextW    = user32.NewProc("GetWindowTextW")
	procGetWindowTextLenW = user32.NewProc("GetWindowTextLengthW")
	procLoadCursorW       = user32.NewProc("LoadCursorW")
	procPostQuitMessage   = user32.NewProc("PostQuitMessage")
	procSendMessageW      = user32.NewProc("SendMessageW")
	procSetFocus          = user32.NewProc("SetFocus")
	procSetWindowTextW    = user32.NewProc("SetWindowTextW")
	procShowWindow        = user32.NewProc("ShowWindow")
	procTranslateMessage  = user32.NewProc("TranslateMessage")
	procUpdateWindow      = user32.NewProc("UpdateWindow")

	procGetModuleHandleW      = kernel32.NewProc("GetModuleHandleW")
	procSHBrowseForFolderW    = shell32.NewProc("SHBrowseForFolderW")
	procSHGetFolderPathW      = shell32.NewProc("SHGetFolderPathW")
	procSHGetPathFromIDListW  = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree         = ole32.NewProc("CoTaskMemFree")
	procOleInitialize         = ole32.NewProc("OleInitialize")
	procOleUninitialize       = ole32.NewProc("OleUninitialize")
	installWndProcCallback    = syscall.NewCallback(installWindowProc)
	browseFolderProcCallback  = syscall.NewCallback(browseFolderCallback)
	currentInstallDialogState *installDialogState
)

func showInstallOptions(defaults installOptions) (installOptions, bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	defaults.InstallDir = filepath.Clean(os.ExpandEnv(defaults.InstallDir))
	state := &installDialogState{
		defaults: defaults,
		result:   defaults,
	}
	currentInstallDialogState = state
	defer func() {
		currentInstallDialogState = nil
	}()

	instance, err := moduleHandle()
	if err != nil {
		return defaults, false, err
	}
	className := "SafeLinkInstallerOptions"
	if err := registerInstallWindowClass(instance, className); err != nil {
		return defaults, false, err
	}

	width := int32(680)
	height := int32(420)
	x := (int32(systemMetric(smCXScreen)) - width) / 2
	y := (int32(systemMetric(smCYScreen)) - height) / 2
	if x < 0 {
		x = 100
	}
	if y < 0 {
		y = 100
	}

	hwnd, err := createWindowEx(
		0,
		className,
		"SafeLink 安装程序",
		wsOverlapped|wsCaption|wsSysMenu|wsMinimizeBox,
		x,
		y,
		width,
		height,
		0,
		0,
		instance,
		0,
	)
	if err != nil {
		return defaults, false, err
	}
	state.hwnd = hwnd
	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)

	var message msg
	for {
		ret, _, callErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(ret) == -1 {
			return defaults, false, fmt.Errorf("read installer window message: %w", callErr)
		}
		if ret == 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
		if state.done {
			break
		}
	}

	return state.result, state.accepted, nil
}

func registerInstallWindowClass(instance uintptr, className string) error {
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	cls := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   installWndProcCallback,
		hInstance:     instance,
		hCursor:       cursor,
		hbrBackground: stockObject(whiteBrush),
		lpszClassName: syscall.StringToUTF16Ptr(className),
	}
	ret, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&cls)))
	if ret == 0 {
		if errno, ok := err.(syscall.Errno); !ok || errno != syscall.Errno(1410) {
			return fmt.Errorf("register installer window: %w", err)
		}
	}
	return nil
}

func installWindowProc(hwnd uintptr, message uint32, wParam uintptr, lParam uintptr) uintptr {
	state := currentInstallDialogState
	switch message {
	case wmCreate:
		if state != nil {
			state.createControls(hwnd)
		}
		return 0
	case wmCommand:
		if state != nil {
			if state.handleCommand(hwnd, int(wParam&0xffff)) {
				return 0
			}
		}
	case wmClose:
		if state != nil {
			state.done = true
			state.accepted = false
		}
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	case wmCtlColorStatic, wmCtlColorBtn:
		procSetBkMode.Call(wParam, transparent)
		return stockObject(whiteBrush)
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}

func (s *installDialogState) createControls(hwnd uintptr) {
	s.bodyFont = createUIFont(-16, fwRegular)
	s.titleFont = createUIFont(-30, fwSemiBold)
	s.headingFont = createUIFont(-18, fwSemiBold)
	s.captionFont = createUIFont(-14, fwRegular)

	createLabel(hwnd, appName, 36, 26, 240, 38, s.titleFont)
	createLabel(hwnd, "安装向导", 38, 66, 180, 22, s.captionFont)
	createSeparator(hwnd, 0, 104, 664)

	createLabel(hwnd, "安装位置", 40, 134, 160, 24, s.headingFont)
	createLabel(hwnd, "选择 SafeLink 的安装目录", 40, 162, 260, 20, s.captionFont)

	s.installEdit = createControl("EDIT", s.defaults.InstallDir, wsChild|wsVisible|wsBorder|wsTabStop|esAutoHScroll, 40, 194, 480, 30, hwnd, idInstallDirEdit)
	setControlFont(s.installEdit, s.bodyFont)
	browseButton := createControl("BUTTON", "浏览...", wsChild|wsVisible|wsTabStop|bsPushButton, 536, 193, 96, 32, hwnd, idBrowseButton)
	setControlFont(browseButton, s.bodyFont)

	s.desktopCheck = createControl("BUTTON", "创建桌面快捷方式", wsChild|wsVisible|wsTabStop|bsAutoCheckBox, 40, 244, 220, 24, hwnd, idDesktopCheck)
	setControlFont(s.desktopCheck, s.bodyFont)
	if s.defaults.CreateDesktopShortcut {
		sendMessage(s.desktopCheck, bmSetCheck, bstChecked, 0)
	} else {
		sendMessage(s.desktopCheck, bmSetCheck, bstUnchecked, 0)
	}

	createSeparator(hwnd, 0, 300, 664)
	installButton := createControl("BUTTON", "立即安装", wsChild|wsVisible|wsTabStop|bsDefPushButton, 424, 330, 104, 34, hwnd, idInstallButton)
	cancelButton := createControl("BUTTON", "取消", wsChild|wsVisible|wsTabStop|bsPushButton, 540, 330, 92, 34, hwnd, idCancelButton)
	setControlFont(installButton, s.bodyFont)
	setControlFont(cancelButton, s.bodyFont)
	procSetFocus.Call(s.installEdit)
}

func (s *installDialogState) handleCommand(hwnd uintptr, id int) bool {
	switch id {
	case idBrowseButton:
		current := windowText(s.installEdit)
		selected, ok, err := browseFolder(hwnd, current)
		if err != nil {
			messageBox("SafeLink 安装程序", err.Error(), 0x10)
			return true
		}
		if ok {
			setWindowText(s.installEdit, selected)
		}
		return true
	case idInstallButton:
		installDir := strings.TrimSpace(windowText(s.installEdit))
		if installDir == "" {
			messageBox("SafeLink 安装程序", "请选择安装目录。", 0x30)
			return true
		}
		s.result.InstallDir = installDir
		s.result.CreateDesktopShortcut = sendMessage(s.desktopCheck, bmGetCheck, 0, 0) == bstChecked
		s.result.Quiet = false
		s.accepted = true
		s.done = true
		procDestroyWindow.Call(hwnd)
		return true
	case idCancelButton:
		s.accepted = false
		s.done = true
		procDestroyWindow.Call(hwnd)
		return true
	default:
		return false
	}
}

func browseFolder(owner uintptr, initial string) (string, bool, error) {
	procOleInitialize.Call(0)
	defer procOleUninitialize.Call()

	displayName := make([]uint16, maxPath)
	initial = filepath.Clean(os.ExpandEnv(strings.TrimSpace(initial)))
	initialPtr := syscall.StringToUTF16Ptr(initial)
	info := browseInfo{
		hwndOwner:      owner,
		pszDisplayName: &displayName[0],
		lpszTitle:      syscall.StringToUTF16Ptr("选择 SafeLink 安装目录"),
		ulFlags:        bifReturnOnlyFSDirs | bifUseNewUI,
		lpfn:           browseFolderProcCallback,
		lParam:         uintptr(unsafe.Pointer(initialPtr)),
	}
	pidl, _, err := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&info)))
	if pidl == 0 {
		if err != syscall.Errno(0) {
			return "", false, fmt.Errorf("browse install directory: %w", err)
		}
		return "", false, nil
	}
	defer procCoTaskMemFree.Call(pidl)

	buf := make([]uint16, maxPath)
	ok, _, callErr := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&buf[0])))
	if ok == 0 {
		if callErr != syscall.Errno(0) {
			return "", false, fmt.Errorf("resolve selected directory: %w", callErr)
		}
		return "", false, errors.New("resolve selected directory failed")
	}
	path := syscall.UTF16ToString(buf)
	if filepath.Base(filepath.Clean(path)) != appName {
		path = filepath.Join(path, appName)
	}
	return path, true, nil
}

func browseFolderCallback(hwnd uintptr, message uint32, _ uintptr, data uintptr) uintptr {
	if message == bffmInitialized && data != 0 {
		sendMessage(hwnd, bffmSetSelectionW, 1, data)
	}
	return 0
}

func desktopDir() (string, error) {
	if path, err := shellFolderPath(csidlDesktopDirectory); err == nil && path != "" {
		return path, nil
	}
	if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
		return filepath.Join(userProfile, "Desktop"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate desktop directory: %w", err)
	}
	return filepath.Join(home, "Desktop"), nil
}

func shellFolderPath(csidl int) (string, error) {
	buf := make([]uint16, maxPath)
	ret, _, err := procSHGetFolderPathW.Call(0, uintptr(csidl), 0, 0, uintptr(unsafe.Pointer(&buf[0])))
	if int32(ret) < 0 {
		if err != syscall.Errno(0) {
			return "", err
		}
		return "", fmt.Errorf("SHGetFolderPathW failed: 0x%x", ret)
	}
	return syscall.UTF16ToString(buf), nil
}

func createLabel(parent uintptr, text string, x, y, width, height int32, font uintptr) uintptr {
	style := uint32(wsChild | wsVisible | ssLeft)
	label := createControl("STATIC", text, style, x, y, width, height, parent, 0)
	setControlFont(label, font)
	return label
}

func createSeparator(parent uintptr, x, y, width int32) uintptr {
	return createControl("STATIC", "", wsChild|wsVisible|ssEtchedHorz, x, y, width, 1, parent, 0)
}

func createControl(className, text string, style uint32, x, y, width, height int32, parent uintptr, id int) uintptr {
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(className))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(text))),
		uintptr(style),
		uintptr(x),
		uintptr(y),
		uintptr(width),
		uintptr(height),
		parent,
		uintptr(id),
		0,
		0,
	)
	if hwnd != 0 {
		sendMessage(hwnd, wmSetFont, stockObject(defaultGUIFont), 1)
	}
	return hwnd
}

func createUIFont(height, weight int32) uintptr {
	font, _, _ := procCreateFontW.Call(
		uintptr(height),
		0,
		0,
		0,
		uintptr(weight),
		0,
		0,
		0,
		defaultCharset,
		0,
		0,
		clearTypeQuality,
		0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("Segoe UI"))),
	)
	if font == 0 {
		return stockObject(defaultGUIFont)
	}
	return font
}

func setControlFont(hwnd, font uintptr) {
	if hwnd != 0 && font != 0 {
		sendMessage(hwnd, wmSetFont, font, 1)
	}
}

func createWindowEx(exStyle uint32, className, title string, style uint32, x, y, width, height int32, parent, menu, instance, param uintptr) (uintptr, error) {
	hwnd, _, err := procCreateWindowExW.Call(
		uintptr(exStyle),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(className))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(title))),
		uintptr(style),
		uintptr(x),
		uintptr(y),
		uintptr(width),
		uintptr(height),
		parent,
		menu,
		instance,
		param,
	)
	if hwnd == 0 {
		return 0, fmt.Errorf("create installer window: %w", err)
	}
	return hwnd, nil
}

func moduleHandle() (uintptr, error) {
	instance, _, err := procGetModuleHandleW.Call(0)
	if instance == 0 {
		return 0, fmt.Errorf("get module handle: %w", err)
	}
	return instance, nil
}

func systemMetric(index int) int {
	ret, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int(ret)
}

func stockObject(index int) uintptr {
	ret, _, _ := procGetStockObject.Call(uintptr(index))
	return ret
}

func sendMessage(hwnd uintptr, message uint32, wParam uintptr, lParam uintptr) uintptr {
	ret, _, _ := procSendMessageW.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}

func windowText(hwnd uintptr) string {
	length, _, _ := procGetWindowTextLenW.Call(hwnd)
	buf := make([]uint16, length+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), length+1)
	return syscall.UTF16ToString(buf)
}

func setWindowText(hwnd uintptr, text string) {
	procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(text))))
}
