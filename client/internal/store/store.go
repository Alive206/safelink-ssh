// Package store persists client-side data: tunnels and subscriptions.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/proxysubscription"
)

const (
	SubscriptionKindVPN   = "vpn"
	SubscriptionKindProxy = "proxy"
)

// SubscriptionSource represents a remote subscription URL.
type SubscriptionSource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Format      string `json:"format"`
	Kind        string `json:"kind,omitempty"`
	AutoRefresh bool   `json:"auto_refresh"`
	IntervalMin int    `json:"interval_min"`
	LastRefresh string `json:"last_refresh,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	TunnelCount int    `json:"tunnel_count"`
	NodeCount   int    `json:"node_count,omitempty"`
}

// SSHConnection is a saved account/password SSH connection profile.
type SSHConnection struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// Store persists tunnels and subscriptions to JSON files.
type Store struct {
	mu             sync.Mutex
	dataDir        string
	tunnelsPath    string
	subsPath       string
	proxyNodesPath string
	sshConnsPath   string
}

// New returns a Store rooted at the given data directory.
func New(dataDir string) *Store {
	return &Store{
		dataDir:        dataDir,
		tunnelsPath:    filepath.Join(dataDir, "tunnels.json"),
		subsPath:       filepath.Join(dataDir, "subscriptions.json"),
		proxyNodesPath: filepath.Join(dataDir, "proxy_nodes.json"),
		sshConnsPath:   filepath.Join(dataDir, "ssh_connections.json"),
	}
}

// LoadTunnels reads tunnels.json. Returns empty slice if not found.
func (s *Store) LoadTunnels() ([]config.TunnelCfg, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.tunnelsPath)
	if errors.Is(err, os.ErrNotExist) {
		return []config.TunnelCfg{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.tunnelsPath, err)
	}
	var tunnels []config.TunnelCfg
	if err := json.Unmarshal(data, &tunnels); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.tunnelsPath, err)
	}
	return tunnels, nil
}

// SaveTunnels writes the tunnel list atomically.
func (s *Store) SaveTunnels(tunnels []config.TunnelCfg) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tunnels == nil {
		tunnels = []config.TunnelCfg{}
	}
	data, err := json.MarshalIndent(tunnels, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tunnels: %w", err)
	}
	return s.writeFileAtomic(s.tunnelsPath, data)
}

// LoadSubscriptions reads subscriptions.json.
func (s *Store) LoadSubscriptions() ([]SubscriptionSource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.subsPath)
	if errors.Is(err, os.ErrNotExist) {
		return []SubscriptionSource{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.subsPath, err)
	}
	var sources []SubscriptionSource
	if err := json.Unmarshal(data, &sources); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.subsPath, err)
	}
	return sources, nil
}

// SaveSubscriptions writes the subscription list atomically.
func (s *Store) SaveSubscriptions(sources []SubscriptionSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sources == nil {
		sources = []SubscriptionSource{}
	}
	data, err := json.MarshalIndent(sources, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal subscriptions: %w", err)
	}
	return s.writeFileAtomic(s.subsPath, data)
}

// LoadProxyNodes reads proxy_nodes.json.
func (s *Store) LoadProxyNodes() ([]proxysubscription.ProxyNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.proxyNodesPath)
	if errors.Is(err, os.ErrNotExist) {
		return []proxysubscription.ProxyNode{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.proxyNodesPath, err)
	}
	var nodes []proxysubscription.ProxyNode
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.proxyNodesPath, err)
	}
	return nodes, nil
}

// SaveProxyNodes writes proxy nodes atomically.
func (s *Store) SaveProxyNodes(nodes []proxysubscription.ProxyNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if nodes == nil {
		nodes = []proxysubscription.ProxyNode{}
	}
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal proxy nodes: %w", err)
	}
	return s.writeFileAtomic(s.proxyNodesPath, data)
}

// LoadSSHConnections reads ssh_connections.json.
func (s *Store) LoadSSHConnections() ([]SSHConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.sshConnsPath)
	if errors.Is(err, os.ErrNotExist) {
		return []SSHConnection{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.sshConnsPath, err)
	}
	var conns []SSHConnection
	if err := json.Unmarshal(data, &conns); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.sshConnsPath, err)
	}
	return conns, nil
}

// SaveSSHConnections writes the saved SSH connection list atomically.
func (s *Store) SaveSSHConnections(conns []SSHConnection) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if conns == nil {
		conns = []SSHConnection{}
	}
	data, err := json.MarshalIndent(conns, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ssh connections: %w", err)
	}
	return s.writeFileAtomic(s.sshConnsPath, data)
}

// SaveSSHConnection inserts or updates a saved SSH connection.
func (s *Store) SaveSSHConnection(conn SSHConnection) (SSHConnection, error) {
	if conn.Name == "" {
		return SSHConnection{}, fmt.Errorf("ssh connection name is required")
	}
	if conn.Addr == "" {
		return SSHConnection{}, fmt.Errorf("ssh connection addr is required")
	}
	if conn.User == "" {
		return SSHConnection{}, fmt.Errorf("ssh connection user is required")
	}
	if conn.Password == "" {
		return SSHConnection{}, fmt.Errorf("ssh connection password is required")
	}

	conns, err := s.LoadSSHConnections()
	if err != nil {
		return SSHConnection{}, err
	}
	if conn.ID == "" {
		conn.ID = randomID()
	}
	for i := range conns {
		if conns[i].ID == conn.ID {
			conns[i] = conn
			return conn, s.SaveSSHConnections(conns)
		}
	}
	conns = append(conns, conn)
	return conn, s.SaveSSHConnections(conns)
}

// DeleteSSHConnection removes a saved SSH connection by ID.
func (s *Store) DeleteSSHConnection(id string) error {
	conns, err := s.LoadSSHConnections()
	if err != nil {
		return err
	}
	found := false
	result := make([]SSHConnection, 0, len(conns))
	for _, conn := range conns {
		if conn.ID == id {
			found = true
			continue
		}
		result = append(result, conn)
	}
	if !found {
		return fmt.Errorf("ssh connection %q not found", id)
	}
	return s.SaveSSHConnections(result)
}

// UpsertProxyNodes inserts or replaces proxy nodes by name.
func (s *Store) UpsertProxyNodes(incoming []proxysubscription.ProxyNode) (imported, skipped int, errs []string) {
	nodes, err := s.LoadProxyNodes()
	if err != nil {
		return 0, len(incoming), []string{err.Error()}
	}
	byName := make(map[string]int, len(nodes))
	for i, node := range nodes {
		byName[node.Name] = i
	}
	for _, node := range incoming {
		if node.Name == "" {
			errs = append(errs, "proxy node requires name")
			skipped++
			continue
		}
		if idx, ok := byName[node.Name]; ok {
			if node.ID == "" {
				node.ID = nodes[idx].ID
			}
			nodes[idx] = node
			imported++
			continue
		}
		if node.ID == "" {
			node.ID = randomID()
		}
		byName[node.Name] = len(nodes)
		nodes = append(nodes, node)
		imported++
	}
	if imported == 0 {
		return imported, skipped, errs
	}
	if err := s.SaveProxyNodes(nodes); err != nil {
		errs = append(errs, fmt.Sprintf("persist proxy nodes: %v", err))
	}
	return imported, skipped, errs
}

// AddSubscription appends a new subscription and saves.
func (s *Store) AddSubscription(src SubscriptionSource) error {
	sources, err := s.LoadSubscriptions()
	if err != nil {
		return err
	}
	if src.ID == "" {
		src.ID = randomID()
	}
	if src.Kind == "" {
		src.Kind = SubscriptionKindVPN
	}
	sources = append(sources, src)
	return s.SaveSubscriptions(sources)
}

// DeleteSubscription removes a subscription by ID and saves.
func (s *Store) DeleteSubscription(id string) error {
	sources, err := s.LoadSubscriptions()
	if err != nil {
		return err
	}
	found := false
	result := make([]SubscriptionSource, 0, len(sources))
	var removed SubscriptionSource
	for _, src := range sources {
		if src.ID == id {
			removed = src
			found = true
			continue
		}
		result = append(result, src)
	}
	if !found {
		return fmt.Errorf("subscription %q not found", id)
	}
	if err := s.SaveSubscriptions(result); err != nil {
		return err
	}
	if removed.Kind == SubscriptionKindProxy {
		return s.DeleteProxyNodesBySubscriptionID(id)
	}
	return nil
}

// DeleteProxyNodesBySubscriptionID removes nodes imported from a subscription.
func (s *Store) DeleteProxyNodesBySubscriptionID(id string) error {
	nodes, err := s.LoadProxyNodes()
	if err != nil {
		return err
	}
	result := make([]proxysubscription.ProxyNode, 0, len(nodes))
	for _, node := range nodes {
		if node.SubscriptionID == id {
			continue
		}
		result = append(result, node)
	}
	return s.SaveProxyNodes(result)
}

// NewID returns a random store identifier for callers that need to reference it immediately.
func NewID() string {
	return randomID()
}

func (s *Store) writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
