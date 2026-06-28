// Package main provides Wails bindings (Go methods exposed to the frontend).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/example/safelink/client/internal/manager"
	"github.com/example/safelink/client/internal/proxycore"
	"github.com/example/safelink/client/internal/sshsession"
	"github.com/example/safelink/client/internal/store"
	"github.com/example/safelink/client/internal/subscriptionclient"
	"github.com/example/safelink/client/internal/tunnel"
	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/proxysubscription"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct holds the application state exposed to the Wails frontend.
type App struct {
	ctx     context.Context
	cancel  context.CancelFunc
	manager *manager.Manager
	store   *store.Store
	sshTerm *sshsession.Manager
	proxy   *proxycore.Runner
	log     *slog.Logger
}

// NewApp creates a new App application struct.
func NewApp() *App {
	return &App{
		log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
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
	a.proxy = proxycore.New(proxycore.Options{
		CorePath: proxycore.FindCorePath(filepath.Dir(os.Args[0])),
		WorkDir:  filepath.Join(dataDir, "proxy"),
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
	if a.manager != nil {
		if ok := a.manager.StopAllTimeout(5 * time.Second); !ok {
			a.log.Warn("timed out waiting for tunnels to stop during shutdown")
		}
	}
	if a.sshTerm != nil {
		a.sshTerm.CloseAll()
	}
	if a.proxy != nil {
		_ = a.proxy.Stop()
	}
	if a.cancel != nil {
		a.cancel()
	}
}

// --- Tunnel Management (exposed to frontend) ---

// ListTunnels returns all tunnel statuses.
func (a *App) ListTunnels() []manager.Status {
	return a.manager.List()
}

// AddTunnel adds and starts a new tunnel.
func (a *App) AddTunnel(cfg config.TunnelCfg) error {
	return a.manager.Add(cfg)
}

// UpdateTunnel updates an existing tunnel config.
func (a *App) UpdateTunnel(name string, cfg config.TunnelCfg) error {
	return a.manager.Update(name, cfg)
}

// DeleteTunnel removes a tunnel.
func (a *App) DeleteTunnel(name string) error {
	return a.manager.Delete(name)
}

// StartTunnel starts a stopped tunnel.
func (a *App) StartTunnel(name string) error {
	return a.manager.StartTunnel(name)
}

// StopTunnel stops a running tunnel.
func (a *App) StopTunnel(name string) error {
	return a.manager.Stop(name)
}

// RestartTunnel restarts a tunnel.
func (a *App) RestartTunnel(name string) error {
	return a.manager.Restart(name)
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

// ListSSHConnections returns saved SSH terminal connection profiles.
func (a *App) ListSSHConnections() ([]store.SSHConnection, error) {
	return a.store.LoadSSHConnections()
}

// SaveSSHConnection creates or updates a saved SSH terminal connection profile.
func (a *App) SaveSSHConnection(conn store.SSHConnection) (store.SSHConnection, error) {
	return a.store.SaveSSHConnection(conn)
}

// DeleteSSHConnection removes a saved SSH terminal connection profile.
func (a *App) DeleteSSHConnection(id string) error {
	return a.store.DeleteSSHConnection(id)
}

// --- Subscription Management ---

// ImportSubscription adds a subscription URL and fetches its tunnels.
func (a *App) ImportSubscription(name, url string) error {
	_, err := subscriptionclient.Import(a.ctx, a.manager, a.store, name, url)
	return err
}

// ListSubscriptions returns all subscription sources.
func (a *App) ListSubscriptions() ([]store.SubscriptionSource, error) {
	return a.store.LoadSubscriptions()
}

// DeleteSubscription removes a subscription by ID.
func (a *App) DeleteSubscription(id string) error {
	return a.store.DeleteSubscription(id)
}

// ListProxyNodes returns all proxy nodes imported from mainstream subscriptions.
func (a *App) ListProxyNodes() ([]proxysubscription.ProxyNode, error) {
	return a.store.LoadProxyNodes()
}

// StartProxyNode starts sing-box with a selected proxy node.
func (a *App) StartProxyNode(nodeName string) error {
	nodes, err := a.store.LoadProxyNodes()
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if node.Name == nodeName {
			return a.proxy.Start(a.ctx, node)
		}
	}
	return fmt.Errorf("proxy node %q not found", nodeName)
}

// StopProxy stops the running sing-box process.
func (a *App) StopProxy() error {
	return a.proxy.Stop()
}

// ProxyStatus returns the current sing-box runner status.
func (a *App) ProxyStatus() proxycore.Status {
	return a.proxy.Status()
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

// GetVersion returns the application version.
func (a *App) GetVersion() string {
	return "1.0.0"
}

// GetDataDir returns the data directory path.
func (a *App) GetDataDir() string {
	return defaultDataDir()
}

// BulkImportTunnels imports a batch of tunnel configs (from subscription).
func (a *App) BulkImportTunnels(tunnels []config.TunnelCfg, prefix string) (int, int, []string) {
	return a.manager.BulkMerge(tunnels, prefix)
}

// Greet is a simple test binding.
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, SafeLink is running!", name)
}
