package tunnel

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DriverStatus describes the TUN driver state on the current system.
type DriverStatus struct {
	OS              string `json:"os"`
	Installed       bool   `json:"installed"`
	DriverPath      string `json:"driver_path,omitempty"`
	Message         string `json:"message"`
	CanAutoFix      bool   `json:"can_auto_fix"`
	IsAdmin         bool   `json:"is_admin"`
	CanRequestAdmin bool   `json:"can_request_admin"`
}

// CheckDriver detects whether the TUN driver is available.
func CheckDriver() (*DriverStatus, error) {
	st := &DriverStatus{OS: runtime.GOOS}

	switch runtime.GOOS {
	case "linux", "darwin":
		st.Installed = true
		st.Message = "TUN ready (kernel built-in)"
		st.CanAutoFix = false
		return st, nil

	case "windows":
		st.IsAdmin = IsRunningAsAdmin()
		st.CanRequestAdmin = !st.IsAdmin

		dirs := []string{
			filepath.Dir(os.Args[0]),
			".",
			os.Getenv("SYSTEMROOT") + "\\System32",
		}
		for _, d := range dirs {
			if d == "" {
				continue
			}
			p := filepath.Join(d, "wintun.dll")
			if _, err := os.Stat(p); err == nil {
				st.Installed = true
				st.DriverPath = p
				if st.IsAdmin {
					st.Message = "Wintun driver installed"
				} else {
					st.Message = "Wintun driver installed, but administrator privileges are required to create TUN device"
				}
				st.CanAutoFix = !st.IsAdmin
				return st, nil
			}
		}
		st.Installed = false
		st.Message = "Wintun driver not found (wintun.dll)"
		st.CanAutoFix = true
		return st, nil

	default:
		st.Message = fmt.Sprintf("unsupported platform: %s", runtime.GOOS)
		return st, nil
	}
}

// InstallDriver downloads and installs Wintun on Windows.
func InstallDriver() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("auto install only supported on Windows")
	}
	st, _ := CheckDriver()
	if st.Installed {
		return nil
	}

	zipURL := "https://www.wintun.net/builds/wintun-0.14.1.zip"
	zipPath := filepath.Join(os.TempDir(), "wintun.zip")
	defer os.Remove(zipPath)

	if err := downloadFile(zipPath, zipURL); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	binDir := filepath.Dir(os.Args[0])
	dst := filepath.Join(binDir, "wintun.dll")

	if err := extractFromZip(zipPath, "wintun/bin/amd64/wintun.dll", dst); err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}
	return nil
}

func downloadFile(path, url string) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	_, err = io.Copy(out, resp.Body)
	return err
}

func extractFromZip(zipPath, insidePath, dst string) error {
	tmp := filepath.Join(os.TempDir(), "wintun_extract")
	defer os.RemoveAll(tmp)

	psCmd := fmt.Sprintf(
		`Expand-Archive -Path '%s' -DestinationPath '%s' -Force; `+
			`Copy-Item '%s\%s' -Destination '%s' -Force; `+
			`Remove-Item -Recurse -Force '%s' -ErrorAction SilentlyContinue`,
		zipPath, tmp,
		tmp, strings.ReplaceAll(insidePath, "/", "\\"),
		dst,
		tmp,
	)

	cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
