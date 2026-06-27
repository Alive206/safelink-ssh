// SafeLink system tray application.
// Manages the safelink daemon lifecycle and provides quick access to the web UI.
package main

import (
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/energye/systray"
)

//go:embed icon.ico
var iconData []byte

var (
	daemonCmd  *exec.Cmd
	daemonLock sync.Mutex
	exePath    string
)

func main() {
	// Determine the directory where this exe lives.
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	exePath = filepath.Dir(exe)

	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(iconData)
	systray.SetTitle("SafeLink")
	systray.SetTooltip("SafeLink SSH Tunnel Manager")

	// Right-click shows menu.
	systray.SetOnRClick(func(menu systray.IMenu) {
		menu.ShowMenu()
	})
	systray.CreateMenu()

	mOpen := systray.AddMenuItem("打开面板", "在浏览器中打开控制面板")
	mOpen.Click(func() {
		openBrowser("http://127.0.0.1:9090")
	})

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("退出", "停止服务并退出")
	mQuit.Click(func() {
		systray.Quit()
	})

	// Start the daemon.
	go startDaemonWithMonitor()
}

func onExit() {
	stopDaemon()
}

// startDaemonWithMonitor starts the daemon and restarts it on crash.
func startDaemonWithMonitor() {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		if err := runDaemon(); err != nil {
			fmt.Fprintf(os.Stderr, "daemon exited: %v, restarting in %v\n", err, backoff)
		}
		time.Sleep(backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func runDaemon() error {
	daemonLock.Lock()

	safelinkExe := filepath.Join(exePath, "safelink.exe")
	cfgPath := filepath.Join(exePath, "configs", "safelink.yaml")

	daemonCmd = exec.Command(safelinkExe, "-config", cfgPath, "-no-open")
	daemonCmd.Dir = exePath
	// Redirect daemon output to a log file.
	logFile, err := os.OpenFile(filepath.Join(exePath, "safelink.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		daemonCmd.Stdout = logFile
		daemonCmd.Stderr = logFile
	}

	if err := daemonCmd.Start(); err != nil {
		daemonLock.Unlock()
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("start daemon: %w", err)
	}
	daemonLock.Unlock()

	// Wait for daemon to exit.
	waitErr := daemonCmd.Wait()
	if logFile != nil {
		logFile.Close()
	}
	return waitErr
}

func stopDaemon() {
	// Try graceful shutdown via API.
	client := &http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequest("POST", "http://127.0.0.1:9090/api/shutdown", nil)
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
		// Wait a moment for graceful exit.
		time.Sleep(2 * time.Second)
	}

	// If still running, force kill.
	daemonLock.Lock()
	defer daemonLock.Unlock()
	if daemonCmd != nil && daemonCmd.Process != nil {
		_ = daemonCmd.Process.Kill()
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
