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
	OS         string `json:"os"`
	Installed  bool   `json:"installed"`
	DriverPath string `json:"driver_path,omitempty"`
	Message    string `json:"message"`
	CanAutoFix bool   `json:"can_auto_fix"`
}

// CheckDriver detects whether the TUN driver is available.
func CheckDriver() (*DriverStatus, error) {
	st := &DriverStatus{OS: runtime.GOOS}

	switch runtime.GOOS {
	case "linux", "darwin":
		_, err := os.Stat("/dev/net/tun")
		if err == nil {
			st.Installed = true
			st.Message = "TUN 已就绪（内核内置）"
		} else {
			st.Installed = true
			st.Message = "TUN 设备可用（内核模块）"
		}
		st.CanAutoFix = false
		return st, nil

	case "windows":
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
				st.Message = "Wintun 驱动已安装"
				st.CanAutoFix = false
				return st, nil
			}
		}
		st.Installed = false
		st.Message = "未检测到 Wintun 驱动（wintun.dll）"
		st.CanAutoFix = true
		return st, nil

	default:
		st.Message = fmt.Sprintf("不支持的平台: %s", runtime.GOOS)
		return st, nil
	}
}

// InstallDriver downloads and installs Wintun on Windows.
func InstallDriver() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("自动安装仅支持 Windows")
	}
	st, _ := CheckDriver()
	if st.Installed {
		return nil
	}

	// Wintun download URL (v0.14.1).
	zipURL := "https://www.wintun.net/builds/wintun-0.14.1.zip"
	zipPath := filepath.Join(os.TempDir(), "wintun.zip")
	defer os.Remove(zipPath)

	// Download.
	if err := downloadFile(zipPath, zipURL); err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}

	// Extract amd64/wintun.dll to the binary dir.
	binDir := filepath.Dir(os.Args[0])
	dst := filepath.Join(binDir, "wintun.dll")

	if err := extractFromZip(zipPath, "wintun/bin/amd64/wintun.dll", dst); err != nil {
		return fmt.Errorf("解压失败: %w", err)
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
	// Use PowerShell's built-in zip handling (available on Win 10+).
	tmp := filepath.Join(os.TempDir(), "wintun_extract")
	defer os.RemoveAll(tmp)

	psCmd := fmt.Sprintf(
		`Expand-Archive -Path '%s' -DestinationPath '%s' -Force; `+
			`Copy-Item '%s\\%s' -Destination '%s' -Force; `+
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
