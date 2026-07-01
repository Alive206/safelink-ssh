//go:build windows && installer

package main

import (
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const (
	appName      = "SafeLink"
	publisher    = "SafeLink"
	mainExe      = "SafeLink.exe"
	uninstallExe = "uninstall.exe"
	installStamp = ".safelink-install"
)

var appVersion = "2.0.0"

//go:embed payload/SafeLink.exe payload/sing-box.exe payload/wintun.dll
var payload embed.FS

type bundledFile struct {
	src  string
	name string
}

type installOptions struct {
	InstallDir            string
	CreateDesktopShortcut bool
	Quiet                 bool
}

var bundledFiles = []bundledFile{
	{src: "payload/SafeLink.exe", name: "SafeLink.exe"},
	{src: "payload/sing-box.exe", name: "sing-box.exe"},
	{src: "payload/wintun.dll", name: "wintun.dll"},
}

func main() {
	args := os.Args[1:]
	if hasArg(args, "/uninstall") || hasArg(args, "--uninstall") {
		if err := uninstall(args); err != nil {
			messageBox("SafeLink 卸载失败", err.Error(), 0x10)
			os.Exit(1)
		}
		return
	}
	if err := install(args); err != nil {
		messageBox("SafeLink 安装失败", err.Error(), 0x10)
		os.Exit(1)
	}
}

func install(args []string) error {
	opts := parseInstallOptions(args)
	if !opts.Quiet {
		selected, ok, err := showInstallOptions(opts)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("installation cancelled")
		}
		opts = selected
	}

	opts.InstallDir = filepath.Clean(os.ExpandEnv(opts.InstallDir))
	if opts.InstallDir == "." || opts.InstallDir == string(filepath.Separator) {
		return fmt.Errorf("invalid install directory: %s", opts.InstallDir)
	}

	if err := os.MkdirAll(opts.InstallDir, 0o755); err != nil {
		return fmt.Errorf("create install directory: %w", err)
	}
	for _, file := range bundledFiles {
		if err := writeBundledFile(file, opts.InstallDir); err != nil {
			return err
		}
	}
	if err := writeStamp(opts.InstallDir); err != nil {
		return err
	}
	if err := copySelf(filepath.Join(opts.InstallDir, uninstallExe)); err != nil {
		return err
	}
	if err := createStartMenuShortcut(opts.InstallDir); err != nil {
		return err
	}
	if opts.CreateDesktopShortcut {
		if err := createDesktopShortcut(opts.InstallDir); err != nil {
			return err
		}
	} else {
		_ = removeDesktopShortcut()
	}
	if err := writeUninstallRegistry(opts.InstallDir); err != nil {
		return err
	}

	if !opts.Quiet {
		messageBox("SafeLink 安装完成", "SafeLink 已安装完成。", 0x40)
	}
	return nil
}

func uninstall(args []string) error {
	installDir, err := installedDirFromRegistry()
	if err != nil || installDir == "" {
		exe, exeErr := os.Executable()
		if exeErr != nil {
			if err != nil {
				return err
			}
			return exeErr
		}
		installDir = filepath.Dir(exe)
	}
	installDir = filepath.Clean(installDir)

	if !isSafeInstallDir(installDir) {
		return fmt.Errorf("refusing to remove unexpected directory: %s", installDir)
	}
	quiet := hasArg(args, "/S") || hasArg(args, "/quiet") || hasArg(args, "--quiet")
	if !quiet && !confirm(fmt.Sprintf("卸载 SafeLink 并删除安装目录：\n\n%s\n\n用户数据目录不会被删除。", installDir)) {
		return errors.New("uninstall cancelled")
	}

	_ = removeStartMenuShortcut()
	_ = removeDesktopShortcut()
	_ = removeRegistryKeys()

	ps := fmt.Sprintf(`
$ProgressPreference = 'SilentlyContinue'
Start-Sleep -Milliseconds 800
$dir = %s
$files = @(%s, %s, %s, %s, %s)
foreach ($file in $files) {
    $path = Join-Path $dir $file
    for ($i = 0; $i -lt 30; $i++) {
        Remove-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue
        if (-not (Test-Path -LiteralPath $path)) { break }
        Start-Sleep -Milliseconds 200
    }
}
for ($i = 0; $i -lt 30; $i++) {
    Remove-Item -LiteralPath $dir -Force -ErrorAction SilentlyContinue
    if (-not (Test-Path -LiteralPath $dir)) { break }
    Start-Sleep -Milliseconds 200
}
`, psQuote(installDir), psQuote(mainExe), psQuote("sing-box.exe"), psQuote("wintun.dll"), psQuote(uninstallExe), psQuote(installStamp))
	if err := runPowerShellDetached(ps); err != nil {
		return fmt.Errorf("schedule install directory removal: %w", err)
	}
	if !quiet {
		messageBox("SafeLink 卸载完成", "SafeLink 已卸载。用户数据目录未删除。", 0x40)
	}
	return nil
}

func writeBundledFile(file bundledFile, installDir string) error {
	data, err := payload.ReadFile(file.src)
	if err != nil {
		return fmt.Errorf("read bundled %s: %w", file.name, err)
	}
	dst := filepath.Join(installDir, file.name)
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return fmt.Errorf("write %s: %w", file.name, err)
	}
	return nil
}

func writeStamp(installDir string) error {
	stamp := fmt.Sprintf("name=%s\nversion=%s\n", appName, appVersion)
	return os.WriteFile(filepath.Join(installDir, installStamp), []byte(stamp), 0o644)
}

func copySelf(dst string) error {
	src, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate installer: %w", err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read installer: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return fmt.Errorf("write uninstaller: %w", err)
	}
	return nil
}

func createShortcut(linkPath, target, workingDir string) error {
	script := fmt.Sprintf(`
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut(%s)
$shortcut.TargetPath = %s
$shortcut.WorkingDirectory = %s
$shortcut.IconLocation = %s
$shortcut.Save()
`, psQuote(linkPath), psQuote(target), psQuote(workingDir), psQuote(target+",0"))
	return runPowerShellHidden(script)
}

func createStartMenuShortcut(installDir string) error {
	startMenu := os.Getenv("APPDATA")
	if startMenu == "" {
		return errors.New("APPDATA is not set")
	}
	linkDir := filepath.Join(startMenu, "Microsoft", "Windows", "Start Menu", "Programs", appName)
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		return fmt.Errorf("create start menu directory: %w", err)
	}
	linkPath := filepath.Join(linkDir, appName+".lnk")
	target := filepath.Join(installDir, mainExe)
	if err := createShortcut(linkPath, target, installDir); err != nil {
		return fmt.Errorf("create start menu shortcut: %w", err)
	}
	return nil
}

func createDesktopShortcut(installDir string) error {
	desktop, err := desktopDir()
	if err != nil {
		return err
	}
	linkPath := filepath.Join(desktop, appName+".lnk")
	target := filepath.Join(installDir, mainExe)
	if err := createShortcut(linkPath, target, installDir); err != nil {
		return fmt.Errorf("create desktop shortcut: %w", err)
	}
	return nil
}

func removeStartMenuShortcut() error {
	startMenu := os.Getenv("APPDATA")
	if startMenu == "" {
		return nil
	}
	linkDir := filepath.Join(startMenu, "Microsoft", "Windows", "Start Menu", "Programs", appName)
	return os.RemoveAll(linkDir)
}

func removeDesktopShortcut() error {
	desktop, err := desktopDir()
	if err != nil {
		return nil
	}
	return os.Remove(filepath.Join(desktop, appName+".lnk"))
}

func writeUninstallRegistry(installDir string) error {
	keyPath := `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + appName
	key, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open uninstall registry key: %w", err)
	}
	defer key.Close()

	displayIcon := filepath.Join(installDir, mainExe)
	uninstallCmd := strconv.Quote(filepath.Join(installDir, uninstallExe)) + " /uninstall"
	values := map[string]string{
		"DisplayName":          appName,
		"DisplayVersion":       appVersion,
		"Publisher":            publisher,
		"InstallLocation":      installDir,
		"DisplayIcon":          displayIcon,
		"UninstallString":      uninstallCmd,
		"QuietUninstallString": uninstallCmd + " /quiet",
	}
	for name, value := range values {
		if err := key.SetStringValue(name, value); err != nil {
			return fmt.Errorf("write registry value %s: %w", name, err)
		}
	}
	if err := key.SetDWordValue("NoModify", 1); err != nil {
		return err
	}
	if err := key.SetDWordValue("NoRepair", 1); err != nil {
		return err
	}
	if size, err := installSizeKB(installDir); err == nil {
		_ = key.SetDWordValue("EstimatedSize", uint32(size))
	}

	appPath, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\App Paths\`+mainExe, registry.SET_VALUE)
	if err == nil {
		defer appPath.Close()
		_ = appPath.SetStringValue("", filepath.Join(installDir, mainExe))
		_ = appPath.SetStringValue("Path", installDir)
	}
	return nil
}

func installedDirFromRegistry() (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\`+appName, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	dir, _, err := key.GetStringValue("InstallLocation")
	return dir, err
}

func removeRegistryKeys() error {
	_ = registry.DeleteKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\App Paths\`+mainExe)
	return registry.DeleteKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\`+appName)
}

func installSizeKB(installDir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(installDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return (total + 1023) / 1024, nil
}

func defaultInstallDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(base, "Programs", appName)
}

func isSafeInstallDir(dir string) bool {
	stamp, err := os.ReadFile(filepath.Join(dir, installStamp))
	if err != nil {
		return false
	}
	return strings.Contains(string(stamp), "name="+appName)
}

func parseInstallOptions(args []string) installOptions {
	quiet := hasArg(args, "/S") || hasArg(args, "/quiet") || hasArg(args, "--quiet")
	opts := installOptions{
		InstallDir:            defaultInstallDir(),
		CreateDesktopShortcut: true,
		Quiet:                 quiet,
	}
	if installDir := argValue(args, "/dir="); installDir != "" {
		opts.InstallDir = installDir
	}
	if hasArg(args, "/no-desktop-shortcut") || hasArg(args, "--no-desktop-shortcut") {
		opts.CreateDesktopShortcut = false
	}
	if value := argValue(args, "/desktop-shortcut="); value != "" {
		opts.CreateDesktopShortcut = parseBool(value, true)
	}
	return opts
}

func parseBool(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func runPowerShellHidden(script string) error {
	encoded := encodePowerShell(script)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

func runPowerShellDetached(script string) error {
	encoded := encodePowerShell(script)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-EncodedCommand", encoded)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

func encodePowerShell(script string) string {
	wide := utf16.Encode([]rune(script))
	bytes := make([]byte, len(wide)*2)
	for i, r := range wide {
		bytes[i*2] = byte(r)
		bytes[i*2+1] = byte(r >> 8)
	}
	return base64.StdEncoding.EncodeToString(bytes)
}

func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if strings.EqualFold(arg, want) {
			return true
		}
	}
	return false
}

func argValue(args []string, prefix string) string {
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(arg), strings.ToLower(prefix)) {
			return strings.TrimSpace(arg[len(prefix):])
		}
	}
	return ""
}

func confirm(text string) bool {
	return messageBox("SafeLink 安装程序", text, 0x24) == 6
}

func messageBox(title, text string, flags uint32) int32 {
	user32 := syscall.NewLazyDLL("user32.dll")
	proc := user32.NewProc("MessageBoxW")
	ret, _, _ := proc.Call(
		0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(text))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(title))),
		uintptr(flags),
	)
	return int32(ret)
}
