// Package main provides Wails bindings (Go methods exposed to the frontend).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/example/safelink/client/internal/manager"
	"github.com/example/safelink/client/internal/proxycore"
	"github.com/example/safelink/client/internal/sshsession"
	"github.com/example/safelink/client/internal/store"
	"github.com/example/safelink/client/internal/subscriptionclient"
	"github.com/example/safelink/client/internal/sysproxy"
	"github.com/example/safelink/client/internal/tunnel"
	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/proxysubscription"
	"github.com/getlantern/systray"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct holds the application state exposed to the Wails frontend.
type App struct {
	ctx            context.Context
	cancel         context.CancelFunc
	manager        *manager.Manager
	store          *store.Store
	sshTerm        *sshsession.Manager
	proxy          *proxycore.Runner
	launch         LaunchOptions
	log            *slog.Logger
	proxyCleanupMu sync.Mutex
	shutdownOnce   sync.Once
	logMu          sync.Mutex
	logs           []LogEntry
}

// LaunchOptions captures one-shot UI startup actions requested by a new window.
type LaunchOptions struct {
	SSHConnectionID string `json:"ssh_connection_id"`
}

// LogEntry is a lightweight UI-visible event log.
type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Module  string `json:"module"`
	Message string `json:"message"`
}

// MachineInfo contains basic local machine facts shown in Settings.
type MachineInfo struct {
	Hostname string   `json:"hostname"`
	Username string   `json:"username"`
	OS       string   `json:"os"`
	Arch     string   `json:"arch"`
	CPUCores int      `json:"cpu_cores"`
	IPs      []string `json:"ips"`
}

// NewApp creates a new App application struct.
func NewApp() *App {
	return &App{
		launch: parseLaunchOptions(os.Args[1:]),
		log:    slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
}

func (a *App) registerTray() {
	systray.Register(func() {
		systray.SetIcon(trayIcon)
		systray.SetTitle("SafeLink")
		systray.SetTooltip("SafeLink")

		openItem := systray.AddMenuItem("打开", "显示 SafeLink 主窗口")
		closeProxyItem := systray.AddMenuItem("关闭代理", "断开当前代理并关闭系统代理")
		systray.AddSeparator()
		quitItem := systray.AddMenuItem("退出 SafeLink", "退出 SafeLink")

		go a.handleTrayMenu(openItem, closeProxyItem, quitItem)
	}, nil)
}

func (a *App) handleTrayMenu(openItem, closeProxyItem, quitItem *systray.MenuItem) {
	for {
		select {
		case <-openItem.ClickedCh:
			a.showMainWindow()
		case <-closeProxyItem.ClickedCh:
			if a.proxy == nil || a.store == nil {
				continue
			}
			if err := a.StopProxy(); err != nil {
				a.recordLog("error", "托盘", "关闭代理", err)
			}
		case <-quitItem.ClickedCh:
			a.quitFromTray()
			return
		}
	}
}

func (a *App) showMainWindow() {
	if a.ctx == nil {
		return
	}
	wailsruntime.WindowShow(a.ctx)
	wailsruntime.WindowUnminimise(a.ctx)
}

func (a *App) quitFromTray() {
	a.closeProxyForExit()
	if a.ctx == nil {
		systray.Quit()
		return
	}
	wailsruntime.Quit(a.ctx)
}

// startup is called when the app starts. The context is saved so we can call
// the runtime methods.
func (a *App) startup(ctx context.Context) {
	rootCtx, cancel := context.WithCancel(ctx)
	a.ctx = rootCtx
	a.cancel = cancel

	// Determine data directory.
	dataDir := defaultDataDir()
	_ = os.MkdirAll(dataDir, 0o755)

	a.store = store.New(dataDir)
	settings, _ := a.store.LoadSettings()
	a.proxy = proxycore.New(proxycore.Options{
		CorePath:      proxycore.FindCorePath(filepath.Dir(os.Args[0])),
		WorkDir:       filepath.Join(dataDir, "proxy"),
		Mode:          settings.ProxyMode,
		RuleModeRules: settings.RuleModeRules,
	})
	tunnels, _ := a.store.LoadTunnels()
	defaults := config.ConnDefaults{
		KeepAliveInterval: 30_000_000_000, // 30s
		KeepAliveMaxFails: 3,
		DialTimeout:       10_000_000_000, // 10s
		ReconnectInitial:  1_000_000_000,  // 1s
		ReconnectMax:      60_000_000_000, // 60s
	}
	a.manager = manager.New(tunnels, defaults, a.log, a.store)
	a.sshTerm = sshsession.NewManager(sshsession.Options{
		OnOutput: func(event sshsession.OutputEvent) {
			wailsruntime.EventsEmit(rootCtx, "ssh:output", event)
		},
		OnClosed: func(event sshsession.ClosedEvent) {
			wailsruntime.EventsEmit(rootCtx, "ssh:closed", event)
		},
		OnError: func(event sshsession.ErrorEvent) {
			wailsruntime.EventsEmit(rootCtx, "ssh:error", event)
		},
	})
	a.manager.Start(rootCtx)
}

// shutdown is called when the app is closing.
func (a *App) shutdown(_ context.Context) {
	a.shutdownOnce.Do(func() {
		a.closeProxyForExit()
		if a.manager != nil {
			if ok := a.manager.StopAllTimeout(5 * time.Second); !ok {
				a.log.Warn("timed out waiting for tunnels to stop during shutdown")
			}
		}
		if a.sshTerm != nil {
			a.sshTerm.CloseAll()
		}
		systray.Quit()
		if a.cancel != nil {
			a.cancel()
		}
	})
}

func (a *App) closeProxyForExit() {
	if a == nil {
		return
	}
	a.proxyCleanupMu.Lock()
	defer a.proxyCleanupMu.Unlock()

	var settings store.ClientSettings
	settingsLoaded := false
	if a.store != nil {
		var err error
		settings, err = a.store.LoadSettings()
		if err != nil {
			a.log.Warn("load settings before proxy cleanup", "err", err)
		} else {
			settingsLoaded = true
		}
	}

	disableSystemProxy := settingsLoaded && settings.SystemProxy
	if !settingsLoaded && a.proxy != nil && a.proxy.Status().State == proxycore.StateRunning {
		disableSystemProxy = true
	}
	if disableSystemProxy {
		if err := sysproxy.SetSystemProxy(false, "", ""); err != nil {
			a.log.Warn("disable system proxy during proxy cleanup", "err", err)
		} else if settingsLoaded && settings.SystemProxy {
			settings.SystemProxy = false
			if err := a.store.SaveSettings(settings); err != nil {
				a.log.Warn("save settings after proxy cleanup", "err", err)
			}
		}
	}

	if a.proxy != nil {
		if err := a.proxy.Stop(); err != nil {
			a.log.Warn("stop proxy core during proxy cleanup", "err", err)
		}
	}
}

// --- Tunnel Management (exposed to frontend) ---

// ListTunnels returns all tunnel statuses.
func (a *App) ListTunnels() []manager.Status {
	return a.manager.List()
}

// AddTunnel adds and starts a new tunnel.
func (a *App) AddTunnel(cfg config.TunnelCfg) error {
	err := a.manager.Add(cfg)
	a.recordLog("info", "SSH 隧道", fmt.Sprintf("新增隧道：%s", cfg.Name), err)
	return err
}

// UpdateTunnel updates an existing tunnel config.
func (a *App) UpdateTunnel(name string, cfg config.TunnelCfg) error {
	err := a.manager.Update(name, cfg)
	a.recordLog("info", "SSH 隧道", fmt.Sprintf("更新隧道：%s", name), err)
	return err
}

// DeleteTunnel removes a tunnel.
func (a *App) DeleteTunnel(name string) error {
	err := a.manager.Delete(name)
	a.recordLog("info", "SSH 隧道", fmt.Sprintf("删除隧道：%s", name), err)
	return err
}

// StartTunnel starts a stopped tunnel.
func (a *App) StartTunnel(name string) error {
	err := a.manager.StartTunnel(name)
	a.recordLog("info", "SSH 隧道", fmt.Sprintf("启动隧道：%s", name), err)
	return err
}

// StopTunnel stops a running tunnel.
func (a *App) StopTunnel(name string) error {
	err := a.manager.Stop(name)
	a.recordLog("info", "SSH 隧道", fmt.Sprintf("停止隧道：%s", name), err)
	return err
}

// RestartTunnel restarts a tunnel.
func (a *App) RestartTunnel(name string) error {
	err := a.manager.Restart(name)
	a.recordLog("info", "SSH 隧道", fmt.Sprintf("重启隧道：%s", name), err)
	return err
}

// ToggleRoute enables or disables the global route for a VPN tunnel.
func (a *App) ToggleRoute(name string, enable bool) error {
	return a.manager.SetVPNRoute(name, enable)
}

// GetStats returns per-tunnel traffic statistics.
func (a *App) GetStats() map[string]tunnel.Snapshot {
	return a.manager.Stats()
}

// --- SSH Terminal Sessions ---

// CreateSSHSession opens an interactive SSH PTY session and returns its ID.
func (a *App) CreateSSHSession(cfg sshsession.Config) (string, error) {
	return a.sshTerm.Create(a.ctx, cfg)
}

// SendSSHInput writes raw terminal input to an interactive SSH session.
func (a *App) SendSSHInput(sessionID, data string) error {
	return a.sshTerm.Write(sessionID, data)
}

// ResizeSSHSession updates the remote PTY dimensions.
func (a *App) ResizeSSHSession(sessionID string, rows, cols int) error {
	return a.sshTerm.Resize(sessionID, rows, cols)
}

// CloseSSHSession closes an interactive SSH session.
func (a *App) CloseSSHSession(sessionID string) error {
	return a.sshTerm.Close(sessionID)
}

// GetLaunchOptions returns startup options passed to this app window.
func (a *App) GetLaunchOptions() LaunchOptions {
	return a.launch
}

// OpenSSHConnectionWindow launches a new app window focused on one SSH connection.
func (a *App) OpenSSHConnectionWindow(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("ssh connection id is required")
	}
	conns, err := a.store.LoadSSHConnections()
	if err != nil {
		return err
	}
	found := false
	for _, conn := range conns {
		if conn.ID == id {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("ssh connection %q not found", id)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "--ssh-connection-id="+id)
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		a.recordLog("error", "SSH 终端", fmt.Sprintf("新建窗口：%s", id), err)
		return err
	}
	a.recordLog("info", "SSH 终端", fmt.Sprintf("新建窗口：%s", id), nil)
	return nil
}

// ListSSHConnections returns saved SSH terminal connection profiles.
func (a *App) ListSSHConnections() ([]store.SSHConnection, error) {
	return a.store.LoadSSHConnections()
}

// SaveSSHConnection creates or updates a saved SSH terminal connection profile.
func (a *App) SaveSSHConnection(conn store.SSHConnection) (store.SSHConnection, error) {
	saved, err := a.store.SaveSSHConnection(conn)
	a.recordLog("info", "SSH 终端", fmt.Sprintf("保存连接：%s", conn.Name), err)
	return saved, err
}

// DeleteSSHConnection removes a saved SSH terminal connection profile.
func (a *App) DeleteSSHConnection(id string) error {
	err := a.store.DeleteSSHConnection(id)
	a.recordLog("info", "SSH 终端", fmt.Sprintf("删除连接：%s", id), err)
	return err
}

// --- Subscription Management ---

// ImportSubscription adds a subscription URL and fetches its tunnels.
func (a *App) ImportSubscription(name, url string) error {
	result, err := subscriptionclient.Import(a.ctx, a.manager, a.store, name, url)
	message := fmt.Sprintf("导入订阅：%s", name)
	if err == nil {
		message = fmt.Sprintf("导入订阅：%s（%s，%d 条）", name, result.Kind, result.Imported)
	}
	a.recordLog("info", "订阅", message, err)
	return err
}

// RefreshSubscription refreshes a stored subscription by ID.
func (a *App) RefreshSubscription(id string) error {
	result, err := subscriptionclient.Refresh(a.ctx, a.manager, a.store, id)
	message := fmt.Sprintf("刷新订阅：%s", id)
	if err == nil {
		message = fmt.Sprintf("刷新订阅：%s（%d 条）", result.Source.Name, result.Imported)
	}
	a.recordLog("info", "订阅", message, err)
	return err
}

// RefreshAllSubscriptions refreshes all stored subscriptions.
func (a *App) RefreshAllSubscriptions() (int, int, []string) {
	imported, skipped, errs := subscriptionclient.RefreshAll(a.ctx, a.manager, a.store)
	a.recordLog("info", "订阅", fmt.Sprintf("刷新全部订阅：导入 %d 条，跳过 %d 条", imported, skipped), nil)
	return imported, skipped, errs
}

// UpdateSubscription updates subscription metadata and refreshes it.
func (a *App) UpdateSubscription(id, name, url string, autoRefresh bool, intervalMin int) error {
	result, err := subscriptionclient.Update(a.ctx, a.manager, a.store, id, name, url, autoRefresh, intervalMin)
	message := fmt.Sprintf("更新订阅：%s", name)
	if err == nil {
		message = fmt.Sprintf("更新订阅：%s（%d 条）", result.Source.Name, result.Imported)
	}
	a.recordLog("info", "订阅", message, err)
	return err
}

// ToggleSubscription enables or disables a subscription as an active node source.
func (a *App) ToggleSubscription(id string, enabled bool) error {
	src, err := a.store.SetSubscriptionEnabled(id, enabled)
	if err != nil {
		a.recordLog("error", "订阅", fmt.Sprintf("切换订阅：%s", id), err)
		return err
	}
	if !enabled {
		if err := a.stopProxyIfSubscriptionDisabled(id); err != nil {
			a.recordLog("error", "订阅", fmt.Sprintf("停用订阅：%s", src.Name), err)
			return err
		}
	}
	state := "停用"
	if enabled {
		state = "启用"
	}
	a.recordLog("info", "订阅", fmt.Sprintf("%s订阅：%s", state, src.Name), nil)
	return nil
}

// ListSubscriptions returns all subscription sources.
func (a *App) ListSubscriptions() ([]store.SubscriptionSource, error) {
	return a.store.LoadSubscriptions()
}

// DeleteSubscription removes a subscription by ID.
func (a *App) DeleteSubscription(id string) error {
	err := a.store.DeleteSubscription(id)
	a.recordLog("info", "订阅", fmt.Sprintf("删除订阅：%s", id), err)
	return err
}

// ListProxyNodes returns all proxy nodes imported from mainstream subscriptions.
func (a *App) ListProxyNodes() ([]proxysubscription.ProxyNode, error) {
	return a.store.LoadActiveProxyNodes()
}

// GetClientSettings returns locally persisted proxy and startup settings.
func (a *App) GetClientSettings() (store.ClientSettings, error) {
	settings, err := a.store.LoadSettings()
	if err != nil {
		return store.ClientSettings{}, err
	}
	if a.proxy != nil {
		settings = a.reconcileSystemProxySetting(settings, a.proxy.Status())
	}
	return settings, nil
}

// SetProxyMode changes the sing-box routing mode and restarts the running node if needed.
func (a *App) SetProxyMode(mode string) error {
	mode = proxycore.NormalizeMode(mode)
	settings, err := a.store.LoadSettings()
	if err != nil {
		return err
	}
	settings.ProxyMode = mode
	if err := a.store.SaveSettings(settings); err != nil {
		return err
	}
	a.proxy.SetMode(mode)
	a.proxy.SetRuleModeRules(settings.RuleModeRules)

	status := a.proxy.Status()
	if status.State == proxycore.StateRunning && status.NodeName != "" {
		if err := a.restartProxyNode(status.NodeName, settings); err != nil {
			a.recordLog("error", "代理模式", fmt.Sprintf("切换模式：%s", mode), err)
			return err
		}
	}
	a.recordLog("info", "代理模式", fmt.Sprintf("切换模式：%s", proxyModeLabel(mode)), nil)
	return nil
}

// SetProxyRules replaces editable route rules used by rule-mode proxy routing.
func (a *App) SetProxyRules(rules []config.ProxyRule) error {
	rules = config.NormalizeProxyRules(rules)
	if err := config.ValidateProxyRules(rules); err != nil {
		return err
	}
	settings, err := a.store.LoadSettings()
	if err != nil {
		return err
	}
	settings.RuleModeRules = rules
	if err := a.store.SaveSettings(settings); err != nil {
		return err
	}
	a.proxy.SetRuleModeRules(rules)

	status := a.proxy.Status()
	if status.State == proxycore.StateRunning && status.NodeName != "" && proxycore.NormalizeMode(settings.ProxyMode) == proxycore.ProxyModeRule {
		if err := a.restartProxyNode(status.NodeName, settings); err != nil {
			a.recordLog("error", "路由规则", "保存路由规则", err)
			return err
		}
	}
	a.recordLog("info", "路由规则", fmt.Sprintf("保存路由规则：%d 条", len(rules)), nil)
	return nil
}

// SetSystemProxyEnabled enables or disables the OS system proxy.
func (a *App) SetSystemProxyEnabled(enabled bool) error {
	settings, err := a.store.LoadSettings()
	if err != nil {
		return err
	}
	status := a.proxy.Status()
	if enabled && status.State != proxycore.StateRunning {
		return errors.New("请先连接节点，再启用系统代理")
	}
	if err := sysproxy.SetSystemProxy(enabled, status.HTTPAddr, status.SOCKSAddr); err != nil {
		a.recordLog("error", "系统代理", "切换系统代理", err)
		return err
	}
	settings.SystemProxy = enabled
	if err := a.store.SaveSettings(settings); err != nil {
		return err
	}
	state := "关闭"
	if enabled {
		state = "开启"
	}
	a.recordLog("info", "系统代理", state+"系统代理", nil)
	return nil
}

// SetAutoStartEnabled adds or removes SafeLink from the user startup entries.
func (a *App) SetAutoStartEnabled(enabled bool) error {
	settings, err := a.store.LoadSettings()
	if err != nil {
		return err
	}
	if err := sysproxy.SetAutoStart(enabled); err != nil {
		a.recordLog("error", "启动项", "切换开机自启", err)
		return err
	}
	settings.AutoStart = enabled
	if err := a.store.SaveSettings(settings); err != nil {
		return err
	}
	state := "关闭"
	if enabled {
		state = "开启"
	}
	a.recordLog("info", "启动项", state+"开机自启", nil)
	return nil
}

// StartProxyNode starts sing-box with a selected proxy node.
func (a *App) StartProxyNode(nodeName string) error {
	nodes, err := a.store.LoadActiveProxyNodes()
	if err != nil {
		return err
	}
	settings, err := a.store.LoadSettings()
	if err != nil {
		return err
	}
	a.proxy.SetMode(settings.ProxyMode)
	a.proxy.SetRuleModeRules(settings.RuleModeRules)
	for _, node := range nodes {
		if node.Name == nodeName {
			if status := a.proxy.Status(); status.State == proxycore.StateRunning {
				if status.NodeName == node.Name {
					return a.enableSystemProxy(&settings)
				}
				if err := a.proxy.Stop(); err != nil {
					return err
				}
			}
			err := a.proxy.Start(a.ctx, node)
			if err == nil {
				if proxyErr := a.enableSystemProxy(&settings); proxyErr != nil {
					_ = a.proxy.Stop()
					err = proxyErr
				}
			}
			a.recordLog("info", "节点", fmt.Sprintf("切换节点：%s", node.Name), err)
			return err
		}
	}
	err = fmt.Errorf("proxy node %q not found", nodeName)
	a.recordLog("error", "节点", fmt.Sprintf("切换节点：%s", nodeName), err)
	return err
}

// TestProxyNode measures HTTP latency through a selected proxy node.
func (a *App) TestProxyNode(nodeName string) proxycore.TestResult {
	nodes, err := a.store.LoadActiveProxyNodes()
	if err != nil {
		return proxycore.TestResult{NodeName: nodeName, OK: false, Error: err.Error(), TestedAt: time.Now().Format(time.RFC3339)}
	}
	for _, node := range nodes {
		if node.Name == nodeName {
			result := a.proxy.Test(a.ctx, node, 8*time.Second)
			if result.OK {
				a.recordLog("info", "节点", fmt.Sprintf("测试节点：%s，延迟 %d ms", node.Name, result.LatencyMS), nil)
			} else {
				a.recordLog("error", "节点", fmt.Sprintf("测试节点：%s", node.Name), errors.New(result.Error))
			}
			return result
		}
	}
	err = fmt.Errorf("proxy node %q not found", nodeName)
	a.recordLog("error", "节点", fmt.Sprintf("测试节点：%s", nodeName), err)
	return proxycore.TestResult{NodeName: nodeName, OK: false, Error: err.Error(), TestedAt: time.Now().Format(time.RFC3339)}
}

// StopProxy stops the running sing-box process.
func (a *App) StopProxy() error {
	settings, settingsErr := a.store.LoadSettings()
	if settingsErr == nil && settings.SystemProxy {
		if err := sysproxy.SetSystemProxy(false, "", ""); err != nil {
			a.recordLog("error", "系统代理", "关闭系统代理", err)
			return err
		}
		settings.SystemProxy = false
		if err := a.store.SaveSettings(settings); err != nil {
			return err
		}
	}
	err := a.proxy.Stop()
	a.recordLog("info", "节点", "断开代理连接", err)
	return err
}

// ProxyStatus returns the current sing-box runner status.
func (a *App) ProxyStatus() proxycore.Status {
	status := a.proxy.Status()
	if a.store != nil {
		if settings, err := a.store.LoadSettings(); err == nil {
			a.reconcileSystemProxySetting(settings, status)
		}
	}
	return status
}

func (a *App) restartProxyNode(nodeName string, settings store.ClientSettings) error {
	nodes, err := a.store.LoadActiveProxyNodes()
	if err != nil {
		return err
	}
	var selected *proxysubscription.ProxyNode
	for i := range nodes {
		if nodes[i].Name == nodeName {
			selected = &nodes[i]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("proxy node %q not found", nodeName)
	}
	if err := a.proxy.Stop(); err != nil {
		return err
	}
	a.proxy.SetMode(settings.ProxyMode)
	a.proxy.SetRuleModeRules(settings.RuleModeRules)
	if err := a.proxy.Start(a.ctx, *selected); err != nil {
		return err
	}
	if settings.SystemProxy {
		if err := a.enableSystemProxy(&settings); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) enableSystemProxy(settings *store.ClientSettings) error {
	status := a.proxy.Status()
	if status.State != proxycore.StateRunning {
		return errors.New("请先连接节点，再启用系统代理")
	}
	if err := sysproxy.SetSystemProxy(true, status.HTTPAddr, status.SOCKSAddr); err != nil {
		a.recordLog("error", "系统代理", "开启系统代理", err)
		return err
	}
	if !settings.SystemProxy {
		settings.SystemProxy = true
		if err := a.store.SaveSettings(*settings); err != nil {
			_ = sysproxy.SetSystemProxy(false, "", "")
			settings.SystemProxy = false
			return err
		}
	}
	a.recordLog("info", "系统代理", "开启系统代理", nil)
	return nil
}

func (a *App) reconcileSystemProxySetting(settings store.ClientSettings, status proxycore.Status) store.ClientSettings {
	if !settings.SystemProxy || status.State == proxycore.StateRunning {
		return settings
	}
	if err := sysproxy.SetSystemProxy(false, "", ""); err != nil {
		a.recordLog("error", "系统代理", "代理核心未运行，关闭系统代理", err)
		return settings
	}
	settings.SystemProxy = false
	if err := a.store.SaveSettings(settings); err != nil {
		a.recordLog("error", "系统代理", "同步系统代理状态", err)
		return settings
	}
	a.recordLog("warn", "系统代理", "代理核心未运行，已关闭系统代理", nil)
	return settings
}

func proxyModeLabel(mode string) string {
	switch proxycore.NormalizeMode(mode) {
	case proxycore.ProxyModeGlobal:
		return "全局模式"
	case proxycore.ProxyModeDirect:
		return "直连模式"
	default:
		return "规则模式"
	}
}

func (a *App) stopProxyIfSubscriptionDisabled(subscriptionID string) error {
	status := a.proxy.Status()
	if status.State != proxycore.StateRunning || status.NodeName == "" {
		return nil
	}
	nodes, err := a.store.LoadProxyNodes()
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if node.Name == status.NodeName && node.SubscriptionID == subscriptionID {
			return a.StopProxy()
		}
	}
	return nil
}

// --- Driver Status ---

// CheckDriver returns the TUN driver status.
func (a *App) CheckDriver() (*tunnel.DriverStatus, error) {
	return tunnel.CheckDriver()
}

// InstallDriver installs the WinTUN driver.
func (a *App) InstallDriver() error {
	return tunnel.InstallDriver()
}

// IsRunningAsAdmin reports whether the app has elevated administrator privileges.
func (a *App) IsRunningAsAdmin() bool {
	return tunnel.IsRunningAsAdmin()
}

// RequestAdminRestart relaunches the app with administrator privileges.
func (a *App) RequestAdminRestart() error {
	if err := tunnel.RequestAdminRestart(); err != nil {
		return err
	}

	go func() {
		time.Sleep(300 * time.Millisecond)
		a.closeProxyForExit()
		if a.manager != nil {
			if ok := a.manager.StopAllTimeout(2 * time.Second); !ok {
				a.log.Warn("timed out waiting for tunnels to stop before admin restart")
			}
		}
		wailsruntime.Quit(a.ctx)
	}()

	return nil
}

// IsTUNAccessDenied reports whether an error message indicates missing TUN privileges.
func (a *App) IsTUNAccessDenied(errorMessage string) bool {
	return tunnel.IsTUNAccessDenied(errors.New(errorMessage))
}

// --- Helpers ---

func defaultDataDir() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appData, "SafeLink")
}

func parseLaunchOptions(args []string) LaunchOptions {
	var opts LaunchOptions
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if strings.HasPrefix(arg, "--ssh-connection-id=") {
			opts.SSHConnectionID = strings.TrimSpace(strings.TrimPrefix(arg, "--ssh-connection-id="))
			continue
		}
		if arg == "--ssh-connection-id" && i+1 < len(args) {
			i++
			opts.SSHConnectionID = strings.TrimSpace(args[i])
		}
	}
	return opts
}

// GetVersion returns the application version.
func (a *App) GetVersion() string {
	return "1.0.0"
}

// GetDataDir returns the data directory path.
func (a *App) GetDataDir() string {
	return defaultDataDir()
}

// GetMachineInfo returns basic local machine information for the settings page.
func (a *App) GetMachineInfo() MachineInfo {
	hostname, _ := os.Hostname()
	return MachineInfo{
		Hostname: hostname,
		Username: currentUsername(),
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		CPUCores: runtime.NumCPU(),
		IPs:      localIPAddresses(),
	}
}

// GetLogs returns recent UI-visible event logs.
func (a *App) GetLogs() []LogEntry {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	out := make([]LogEntry, len(a.logs))
	copy(out, a.logs)
	return out
}

// ClearLogs clears UI-visible event logs.
func (a *App) ClearLogs() {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	a.logs = nil
}

// BulkImportTunnels imports a batch of tunnel configs (from subscription).
func (a *App) BulkImportTunnels(tunnels []config.TunnelCfg, prefix string) (int, int, []string) {
	return a.manager.BulkMerge(tunnels, prefix)
}

// Greet is a simple test binding.
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, SafeLink is running!", name)
}

func currentUsername() string {
	user := strings.TrimSpace(os.Getenv("USERNAME"))
	if user == "" {
		user = strings.TrimSpace(os.Getenv("USER"))
	}
	if domain := strings.TrimSpace(os.Getenv("USERDOMAIN")); domain != "" && user != "" {
		return domain + `\` + user
	}
	return user
}

func localIPAddresses() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	ips := make([]string, 0)
	seen := make(map[string]struct{})
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch value := addr.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			text := ip.String()
			if _, ok := seen[text]; ok {
				continue
			}
			seen[text] = struct{}{}
			ips = append(ips, text)
		}
	}
	return ips
}

func (a *App) recordLog(level, module, message string, err error) {
	if err != nil {
		level = "error"
		message = fmt.Sprintf("%s：%v", message, err)
	}
	entry := LogEntry{
		Time:    time.Now().Format(time.RFC3339),
		Level:   level,
		Module:  module,
		Message: message,
	}
	a.logMu.Lock()
	defer a.logMu.Unlock()
	a.logs = append(a.logs, entry)
	if len(a.logs) > 500 {
		a.logs = append([]LogEntry(nil), a.logs[len(a.logs)-500:]...)
	}
}
