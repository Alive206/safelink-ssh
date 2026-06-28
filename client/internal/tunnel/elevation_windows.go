//go:build windows

package tunnel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32           = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
)

type tokenElevation struct {
	TokenIsElevated uint32
}

// IsRunningAsAdmin reports whether the current process has an elevated administrator token.
func IsRunningAsAdmin() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()

	var elevation tokenElevation
	var outLen uint32
	err := windows.GetTokenInformation(
		token,
		windows.TokenElevation,
		(*byte)(unsafe.Pointer(&elevation)),
		uint32(unsafe.Sizeof(elevation)),
		&outLen,
	)
	if err != nil {
		return false
	}
	return elevation.TokenIsElevated != 0
}

// RequestAdminRestart launches a new elevated instance of the current executable.
func RequestAdminRestart() error {
	program, args, err := resolveRestartTarget()
	if err != nil {
		return err
	}
	return shellExecuteRunAs(program, args)
}

func resolveRestartTarget() (program, args string, err error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	return resolveRestartTargetFromExecutable(exe)
}

func resolveRestartTargetFromExecutable(exe string) (program, args string, err error) {
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	} else if !os.IsNotExist(err) {
		return "", "", err
	}

	base := strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe")
	if isDevExecutableName(base) {
		if prod, ok := findBuiltSafeLinkExecutable(filepath.Dir(exe)); ok {
			return prod, "", nil
		}

		return "", "", fmt.Errorf("当前是开发模式，无法直接通过 UAC 重启。请先执行 scripts\\build-client.bat 生成 client\\build\\bin\\SafeLink.exe，或以管理员身份打开 PowerShell 后在 client 目录运行 wails dev")
	}

	return exe, "", nil
}

func isDevExecutableName(base string) bool {
	return base == "safelink-dev" || strings.HasSuffix(base, "-dev")
}

func findBuiltSafeLinkExecutable(startDir string) (string, bool) {
	for _, dir := range candidateProjectDirs(startDir) {
		for _, p := range []string{
			filepath.Join(dir, "SafeLink.exe"),
			filepath.Join(dir, "build", "bin", "SafeLink.exe"),
		} {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, true
			}
		}
	}
	return "", false
}

func candidateProjectDirs(startDir string) []string {
	var dirs []string
	seen := make(map[string]struct{})
	for dir := startDir; dir != ""; dir = filepath.Dir(dir) {
		if _, ok := seen[dir]; !ok {
			dirs = append(dirs, dir)
			seen[dir] = struct{}{}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return dirs
}

func shellExecuteRunAs(program, args string) error {
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(program)
	cwd, _ := syscall.UTF16PtrFromString(filepath.Dir(program))

	var params *uint16
	if args != "" {
		params, _ = syscall.UTF16PtrFromString(args)
	}

	ret, _, callErr := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		uintptr(unsafe.Pointer(cwd)),
		1, // SW_SHOWNORMAL
	)
	if ret <= 32 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return fmt.Errorf("无法请求管理员权限: %w", callErr)
		}
		return fmt.Errorf("无法请求管理员权限 (ShellExecute 返回 %d，可能已取消 UAC 提示)", ret)
	}
	return nil
}

// IsTUNAccessDenied reports whether an error indicates missing privileges for TUN creation.
func IsTUNAccessDenied(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "access is denied") ||
		strings.Contains(msg, "access denied") ||
		strings.Contains(msg, "拒绝访问")
}
