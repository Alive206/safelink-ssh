// Package manager owns the runtime lifecycle of every configured tunnel.
package manager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/example/safelink/client/internal/sshclient"
	"github.com/example/safelink/client/internal/store"
	"github.com/example/safelink/client/internal/tunnel"
	"github.com/example/safelink/pkg/config"
)

var ErrNotFound = errors.New("tunnel not found")
var ErrExists = errors.New("tunnel already exists")

// Status is the JSON-friendly snapshot exposed to the frontend.
type Status struct {
	Config      config.TunnelCfg `json:"config"`
	State       string           `json:"state"`
	LastError   string           `json:"last_error,omitempty"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	UptimeSec   int64            `json:"uptime_seconds"`
	RunCount    int64            `json:"run_count"`
	Stats       tunnel.Snapshot  `json:"stats"`
	RouteActive bool             `json:"route_active,omitempty"`
}

type managedTunnel struct {
	cfg      config.TunnelCfg
	stats    *tunnel.Stats
	cancel   context.CancelFunc
	done     chan struct{}
	state    atomic.Value // sshclient.State
	lastErr  atomic.Value // string
	runCount atomic.Int64
	startAt  atomic.Value // time.Time
	vpn      *tunnel.VPN
}

func (m *managedTunnel) loadState() sshclient.State {
	if v := m.state.Load(); v != nil {
		return v.(sshclient.State)
	}
	return sshclient.StateStopped
}

func (m *managedTunnel) loadErr() string {
	if v := m.lastErr.Load(); v != nil {
		return v.(string)
	}
	return ""
}

func (m *managedTunnel) loadStart() (time.Time, bool) {
	if v := m.startAt.Load(); v != nil {
		t := v.(time.Time)
		return t, !t.IsZero()
	}
	return time.Time{}, false
}

// Manager coordinates a fleet of managedTunnel instances.
type Manager struct {
	mu              sync.RWMutex
	tunnels         map[string]*managedTunnel
	defaults        config.ConnDefaults
	knownHosts      string
	insecureHostKey bool
	log             *slog.Logger
	store           *store.Store
	rootCtx         context.Context
}

// New constructs a Manager seeded from existing tunnels.
func New(cfgs []config.TunnelCfg, defaults config.ConnDefaults, log *slog.Logger, st *store.Store) *Manager {
	m := &Manager{
		tunnels:         make(map[string]*managedTunnel),
		defaults:        defaults,
		insecureHostKey: true,
		log:             log,
		store:           st,
	}
	for _, tc := range cfgs {
		mt := &managedTunnel{cfg: tc, stats: tunnel.NewStats()}
		mt.state.Store(sshclient.StateStopped)
		m.tunnels[tc.Name] = mt
	}
	return m
}

// Start binds the manager to the application lifecycle context.
// Tunnels remain stopped until StartTunnel is called explicitly.
func (m *Manager) Start(ctx context.Context) {
	m.rootCtx = ctx
}

// StopAll cancels every running supervisor.
func (m *Manager) StopAll() {
	m.stopAllWithTimeout(0)
}

// StopAllTimeout cancels every running tunnel and waits up to timeout per tunnel.
// It returns false if any tunnel did not report completion before the timeout.
func (m *Manager) StopAllTimeout(timeout time.Duration) bool {
	return m.stopAllWithTimeout(timeout)
}

func (m *Manager) stopAllWithTimeout(timeout time.Duration) bool {
	m.mu.RLock()
	mts := make([]*managedTunnel, 0, len(m.tunnels))
	for _, mt := range m.tunnels {
		mts = append(mts, mt)
	}
	m.mu.RUnlock()
	ok := true
	for _, mt := range mts {
		if !m.stopManagedWithTimeout(mt, timeout) {
			ok = false
		}
	}
	return ok
}

// List returns all tunnel statuses.
func (m *Manager) List() []Status {
	m.mu.RLock()
	out := make([]Status, 0, len(m.tunnels))
	for _, mt := range m.tunnels {
		out = append(out, m.statusOf(mt))
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].Config.Name < out[j].Config.Name
	})
	return out
}

// Get returns one tunnel's status.
func (m *Manager) Get(name string) (Status, error) {
	m.mu.RLock()
	mt, ok := m.tunnels[name]
	m.mu.RUnlock()
	if !ok {
		return Status{}, ErrNotFound
	}
	return m.statusOf(mt), nil
}

// Stats returns per-tunnel snapshots.
func (m *Manager) Stats() map[string]tunnel.Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]tunnel.Snapshot, len(m.tunnels))
	for n, mt := range m.tunnels {
		out[n] = mt.stats.Snapshot()
	}
	return out
}

// Add validates, inserts, persists, and starts a new tunnel.
func (m *Manager) Add(tc config.TunnelCfg) error {
	if err := config.ValidateTunnel(tc); err != nil {
		return err
	}
	m.mu.Lock()
	if _, exists := m.tunnels[tc.Name]; exists {
		m.mu.Unlock()
		return ErrExists
	}
	mt := &managedTunnel{cfg: tc, stats: tunnel.NewStats()}
	mt.state.Store(sshclient.StateStopped)
	m.tunnels[tc.Name] = mt
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		m.mu.Lock()
		delete(m.tunnels, tc.Name)
		m.mu.Unlock()
		return fmt.Errorf("persist tunnels: %w", err)
	}
	return m.startTunnel(tc.Name)
}

// Update replaces a tunnel's config.
func (m *Manager) Update(name string, tc config.TunnelCfg) error {
	tc.Name = name
	if err := config.ValidateTunnel(tc); err != nil {
		return err
	}
	m.mu.Lock()
	mt, ok := m.tunnels[name]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	old := mt.cfg
	mt.cfg = tc
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		m.mu.Lock()
		mt.cfg = old
		m.mu.Unlock()
		return fmt.Errorf("persist tunnels: %w", err)
	}
	return m.Restart(name)
}

// Delete stops and removes a tunnel.
func (m *Manager) Delete(name string) error {
	m.mu.Lock()
	mt, ok := m.tunnels[name]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	delete(m.tunnels, name)
	m.mu.Unlock()

	m.stopManaged(mt)
	return m.persist()
}

// StartTunnel launches a previously stopped tunnel.
func (m *Manager) StartTunnel(name string) error { return m.startTunnel(name) }

// Stop cancels a running tunnel without removing it.
func (m *Manager) Stop(name string) error {
	m.mu.RLock()
	mt, ok := m.tunnels[name]
	m.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	m.stopManaged(mt)
	return nil
}

// Restart is Stop + Start.
func (m *Manager) Restart(name string) error {
	if err := m.Stop(name); err != nil {
		return err
	}
	return m.startTunnel(name)
}

// SetVPNRoute dynamically enables/disables the global route for a VPN tunnel.
func (m *Manager) SetVPNRoute(name string, enable bool) error {
	m.mu.RLock()
	mt, ok := m.tunnels[name]
	m.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	if mt.cfg.Mode != config.ModeVPN {
		return fmt.Errorf("tunnel %q is not a VPN tunnel", name)
	}
	if mt.vpn == nil {
		return fmt.Errorf("vpn tunnel %q is not running", name)
	}

	if enable {
		m.mu.RLock()
		for n, other := range m.tunnels {
			if n != name && other.vpn != nil && other.vpn.RouteActive() {
				_ = other.vpn.SetRoute(false)
			}
		}
		m.mu.RUnlock()
	}
	return mt.vpn.SetRoute(enable)
}

// BulkMerge imports a batch of tunnels.
func (m *Manager) BulkMerge(incoming []config.TunnelCfg, prefix string) (imported, skipped int, errs []string) {
	for _, tc := range incoming {
		if prefix != "" {
			tc.Name = prefix + tc.Name
		}
		if _, err := m.Get(tc.Name); err == nil {
			if err := m.Update(tc.Name, tc); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", tc.Name, err))
				skipped++
			} else {
				imported++
			}
		} else {
			if err := m.Add(tc); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", tc.Name, err))
				skipped++
			} else {
				imported++
			}
		}
	}
	return
}

// BulkUpsert imports tunnel configs without starting new tunnels.
func (m *Manager) BulkUpsert(incoming []config.TunnelCfg, prefix string) (imported, skipped int, errs []string) {
	m.mu.Lock()
	for _, tc := range incoming {
		if prefix != "" {
			tc.Name = prefix + tc.Name
		}
		if err := config.ValidateTunnel(tc); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", tc.Name, err))
			skipped++
			continue
		}
		if mt, exists := m.tunnels[tc.Name]; exists {
			if mt.cancel != nil {
				errs = append(errs, fmt.Sprintf("%s: running tunnel was not updated", tc.Name))
				skipped++
				continue
			}
			mt.cfg = tc
			imported++
			continue
		}
		mt := &managedTunnel{cfg: tc, stats: tunnel.NewStats()}
		mt.state.Store(sshclient.StateStopped)
		m.tunnels[tc.Name] = mt
		imported++
	}
	m.mu.Unlock()

	if imported == 0 {
		return imported, skipped, errs
	}
	if err := m.persist(); err != nil {
		errs = append(errs, fmt.Sprintf("persist tunnels: %v", err))
	}
	return imported, skipped, errs
}

// --- internals ---

func (m *Manager) statusOf(mt *managedTunnel) Status {
	st := Status{
		Config:   mt.cfg,
		State:    string(mt.loadState()),
		RunCount: mt.runCount.Load(),
		Stats:    mt.stats.Snapshot(),
	}
	if e := mt.loadErr(); e != "" {
		st.LastError = e
	}
	if t, ok := mt.loadStart(); ok {
		ts := t
		st.StartedAt = &ts
		st.UptimeSec = int64(time.Since(t).Seconds())
	}
	if mt.vpn != nil {
		st.RouteActive = mt.vpn.RouteActive()
	}
	return st
}

func (m *Manager) startTunnel(name string) error {
	if m.rootCtx == nil {
		return errors.New("manager not started yet")
	}
	m.mu.Lock()
	mt, ok := m.tunnels[name]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if mt.cancel != nil {
		m.mu.Unlock()
		return nil
	}
	t, err := tunnel.Build(mt.cfg, m.log, mt.stats)
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("build tunnel: %w", err)
	}

	if mt.cfg.Mode == config.ModeVPN {
		return m.startVPN(mt, t.(*tunnel.VPN))
	}

	sup := sshclient.NewSupervisor(mt.cfg, m.defaults, m.knownHosts, m.insecureHostKey, t, m.log)
	sup.OnStateChange = func(state sshclient.State, lastErr error) {
		mt.state.Store(state)
		if lastErr != nil {
			mt.lastErr.Store(lastErr.Error())
		} else if state == sshclient.StateRunning {
			mt.lastErr.Store("")
		}
		switch state {
		case sshclient.StateRunning:
			mt.startAt.Store(time.Now())
			mt.runCount.Add(1)
		case sshclient.StateStopped:
			mt.startAt.Store(time.Time{})
		}
	}
	ctx, cancel := context.WithCancel(m.rootCtx)
	mt.cancel = cancel
	mt.done = make(chan struct{})
	m.mu.Unlock()

	go func() {
		defer close(mt.done)
		sup.Run(ctx)
		m.mu.Lock()
		if mt.cancel != nil {
			mt.cancel = nil
		}
		m.mu.Unlock()
	}()
	return nil
}

func (m *Manager) startVPN(mt *managedTunnel, vpn *tunnel.VPN) error {
	mt.vpn = vpn
	vpn.OnStateChange = func(state string, lastErr error) {
		mt.state.Store(sshclient.State(state))
		if lastErr != nil {
			mt.lastErr.Store(lastErr.Error())
		} else if state == "running" {
			mt.lastErr.Store("")
		}
		switch state {
		case "running":
			mt.startAt.Store(time.Now())
			mt.runCount.Add(1)
		case "stopped":
			mt.startAt.Store(time.Time{})
		}
	}
	ctx, cancel := context.WithCancel(m.rootCtx)
	mt.cancel = cancel
	mt.done = make(chan struct{})
	m.mu.Unlock()
	go func() {
		defer close(mt.done)
		vpn.Run(ctx)
		m.mu.Lock()
		mt.vpn = nil
		if mt.cancel != nil {
			mt.cancel = nil
		}
		m.mu.Unlock()
	}()
	return nil
}

func (m *Manager) stopManaged(mt *managedTunnel) {
	m.stopManagedWithTimeout(mt, 0)
}

func (m *Manager) stopManagedWithTimeout(mt *managedTunnel, timeout time.Duration) bool {
	m.mu.Lock()
	cancel := mt.cancel
	done := mt.done
	mt.cancel = nil
	m.mu.Unlock()
	if cancel == nil {
		return true
	}
	cancel()
	if done != nil {
		if timeout <= 0 {
			<-done
			return true
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-done:
			return true
		case <-timer.C:
			return false
		}
	}
	return true
}

func (m *Manager) persist() error {
	if m.store == nil {
		return nil
	}
	m.mu.RLock()
	out := make([]config.TunnelCfg, 0, len(m.tunnels))
	for _, mt := range m.tunnels {
		out = append(out, mt.cfg)
	}
	m.mu.RUnlock()
	return m.store.SaveTunnels(out)
}
