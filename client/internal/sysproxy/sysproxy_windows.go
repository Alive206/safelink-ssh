package sysproxy

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	appName             = "SafeLink"
	internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	runKey              = `Software\Microsoft\Windows\CurrentVersion\Run`

	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
)

// SetSystemProxy enables or disables Windows' current-user system proxy.
func SetSystemProxy(enabled bool, httpAddr, socksAddr string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open internet settings: %w", err)
	}
	defer key.Close()

	if !enabled {
		if err := key.SetDWordValue("ProxyEnable", 0); err != nil {
			return fmt.Errorf("disable system proxy: %w", err)
		}
		notifyInternetSettings()
		return nil
	}

	if httpAddr == "" && socksAddr == "" {
		return errors.New("proxy listener is not available")
	}
	if err := key.SetStringValue("ProxyServer", buildProxyServer(httpAddr, socksAddr)); err != nil {
		return fmt.Errorf("set proxy server: %w", err)
	}
	if err := key.SetStringValue("ProxyOverride", "<local>"); err != nil {
		return fmt.Errorf("set proxy bypass: %w", err)
	}
	if err := key.SetDWordValue("ProxyEnable", 1); err != nil {
		return fmt.Errorf("enable system proxy: %w", err)
	}
	notifyInternetSettings()
	return nil
}

// SetAutoStart adds or removes SafeLink from the current-user Run key.
func SetAutoStart(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open startup key: %w", err)
	}
	defer key.Close()

	if !enabled {
		if err := key.DeleteValue(appName); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("remove startup entry: %w", err)
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	if err := key.SetStringValue(appName, fmt.Sprintf("%q", exe)); err != nil {
		return fmt.Errorf("set startup entry: %w", err)
	}
	return nil
}

func buildProxyServer(httpAddr, socksAddr string) string {
	httpEndpoint := normalizeEndpoint(httpAddr)
	socksEndpoint := normalizeEndpoint(socksAddr)
	if httpEndpoint != "" {
		return httpEndpoint
	}
	return fmt.Sprintf("socks=%s", socksEndpoint)
}

func normalizeEndpoint(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func notifyInternetSettings() {
	wininet := windows.NewLazySystemDLL("wininet.dll")
	proc := wininet.NewProc("InternetSetOptionW")
	_, _, _ = proc.Call(0, uintptr(internetOptionSettingsChanged), 0, 0)
	_, _, _ = proc.Call(0, uintptr(internetOptionRefresh), 0, 0)
}
