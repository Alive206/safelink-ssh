package vpnserver

import (
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

// RuntimeConfig is static metadata exposed by the control panel.
type RuntimeConfig struct {
	ListenAddr string
	Subnet     string
	NATIface   string
	NATEnabled bool
	Padding    bool
}

// RuntimeSnapshot is a point-in-time view of the VPN server.
type RuntimeSnapshot struct {
	StartedAt     time.Time        `json:"started_at"`
	UptimeSeconds int64            `json:"uptime_seconds"`
	ListenAddr    string           `json:"listen_addr"`
	Subnet        string           `json:"subnet"`
	NATIface      string           `json:"nat_iface"`
	NATEnabled    bool             `json:"nat_enabled"`
	Padding       bool             `json:"padding"`
	ActiveClients int              `json:"active_clients"`
	TotalClients  uint64           `json:"total_clients"`
	TotalBytesIn  uint64           `json:"total_bytes_in"`
	TotalBytesOut uint64           `json:"total_bytes_out"`
	Clients       []ClientSnapshot `json:"clients"`
}

// ClientSnapshot describes one authenticated VPN client.
type ClientSnapshot struct {
	ID              string    `json:"id"`
	RemoteAddr      string    `json:"remote_addr"`
	ConnectedAt     time.Time `json:"connected_at"`
	UptimeSeconds   int64     `json:"uptime_seconds"`
	BytesIn         uint64    `json:"bytes_in"`
	BytesOut        uint64    `json:"bytes_out"`
	LastTrafficAt   time.Time `json:"last_traffic_at"`
	TUNName         string    `json:"tun_name,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	AuthenticatedAs string    `json:"authenticated_as,omitempty"`
}

// Runtime tracks live VPN clients and traffic counters.
type Runtime struct {
	mu            sync.RWMutex
	cfg           RuntimeConfig
	startedAt     time.Time
	nextClient    uint64
	totalClients  uint64
	totalBytesIn  uint64
	totalBytesOut uint64
	clients       map[string]*clientRuntime
}

type clientRuntime struct {
	id              string
	remoteAddr      string
	connectedAt     time.Time
	bytesIn         uint64
	bytesOut        uint64
	lastTrafficAt   time.Time
	tunName         string
	lastError       string
	authenticatedAs string
}

// NewRuntime creates a runtime registry for a VPN server process.
func NewRuntime(cfg RuntimeConfig) *Runtime {
	return &Runtime{
		cfg:       cfg,
		startedAt: time.Now(),
		clients:   make(map[string]*clientRuntime),
	}
}

// RegisterClient records an authenticated client and returns its runtime id.
func (r *Runtime) RegisterClient(remote net.Addr) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextClient++
	r.totalClients++
	id := fmt.Sprintf("client-%d", r.nextClient)
	now := time.Now()
	remoteAddr := ""
	if remote != nil {
		remoteAddr = remote.String()
	}
	r.clients[id] = &clientRuntime{
		id:            id,
		remoteAddr:    remoteAddr,
		connectedAt:   now,
		lastTrafficAt: now,
	}
	return id
}

// UnregisterClient removes a client from the active set.
func (r *Runtime) UnregisterClient(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, id)
}

// SetClientTUN records the TUN interface allocated for a client.
func (r *Runtime) SetClientTUN(id, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.clients[id]; c != nil {
		c.tunName = name
	}
}

// SetClientUser records the authenticated username for a client.
func (r *Runtime) SetClientUser(id, username string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.clients[id]; c != nil {
		c.authenticatedAs = username
	}
}

// SetClientError records the last connection-level error for a client.
func (r *Runtime) SetClientError(id string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.clients[id]; c != nil && err != nil {
		c.lastError = err.Error()
	}
}

// AddClientTraffic increments per-client and process totals.
func (r *Runtime) AddClientTraffic(id string, bytesIn, bytesOut uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.totalBytesIn += bytesIn
	r.totalBytesOut += bytesOut
	if c := r.clients[id]; c != nil {
		c.bytesIn += bytesIn
		c.bytesOut += bytesOut
		c.lastTrafficAt = time.Now()
	}
}

// Snapshot returns a stable view for API responses.
func (r *Runtime) Snapshot() RuntimeSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := time.Now()
	clients := make([]ClientSnapshot, 0, len(r.clients))
	for _, c := range r.clients {
		clients = append(clients, ClientSnapshot{
			ID:              c.id,
			RemoteAddr:      c.remoteAddr,
			ConnectedAt:     c.connectedAt,
			UptimeSeconds:   int64(now.Sub(c.connectedAt).Seconds()),
			BytesIn:         c.bytesIn,
			BytesOut:        c.bytesOut,
			LastTrafficAt:   c.lastTrafficAt,
			TUNName:         c.tunName,
			LastError:       c.lastError,
			AuthenticatedAs: c.authenticatedAs,
		})
	}
	sort.Slice(clients, func(i, j int) bool {
		return clients[i].ConnectedAt.Before(clients[j].ConnectedAt)
	})
	return RuntimeSnapshot{
		StartedAt:     r.startedAt,
		UptimeSeconds: int64(now.Sub(r.startedAt).Seconds()),
		ListenAddr:    r.cfg.ListenAddr,
		Subnet:        r.cfg.Subnet,
		NATIface:      r.cfg.NATIface,
		NATEnabled:    r.cfg.NATEnabled,
		Padding:       r.cfg.Padding,
		ActiveClients: len(clients),
		TotalClients:  r.totalClients,
		TotalBytesIn:  r.totalBytesIn,
		TotalBytesOut: r.totalBytesOut,
		Clients:       clients,
	}
}
