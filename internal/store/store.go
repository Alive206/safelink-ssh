// Package store persists configuration that the web UI is allowed to mutate.
// Static settings (log_level, defaults, web, known_hosts) live in the main
// YAML; the tunnel list lives in a sibling JSON file so it can be rewritten
// atomically without touching anything else.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/example/sshtunneld/internal/config"
)

// VPNServer represents a deployed VPN server that can be reused when creating tunnels.
type VPNServer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ServerAddr  string `json:"server_addr"`
	ServerPort  string `json:"server_port"`
	Subnet      string `json:"subnet"`
	VPNUser     string `json:"vpn_user"`
	VPNPass     string `json:"vpn_pass"`
	SSHAddr     string `json:"ssh_addr,omitempty"`
	SSHUser     string `json:"ssh_user,omitempty"`
	SSHPassword string `json:"ssh_password,omitempty"`
	EgressIface string `json:"egress_iface,omitempty"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

// SubscriptionSource represents a remote subscription URL the client polls.
type SubscriptionSource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Format      string `json:"format"`       // "auto", "json", "clash"
	AutoRefresh bool   `json:"auto_refresh"` // periodic refresh
	IntervalMin int    `json:"interval_min"` // refresh interval in minutes
	LastRefresh string `json:"last_refresh,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	TunnelCount int    `json:"tunnel_count"`
}

// Store binds the immutable main YAML to the writable tunnels.json file.
type Store struct {
	mu          sync.Mutex // serialises writes to tunnelsPath
	mainPath    string
	tunnelsPath string
	serversPath string
	subsPath    string
}

// New returns a Store rooted at mainPath; tunnels.json is co-located in the
// same directory.  The JSON file is created lazily on first save.
func New(mainPath string) *Store {
	dir := filepath.Dir(mainPath)
	return &Store{
		mainPath:    mainPath,
		tunnelsPath: filepath.Join(dir, "tunnels.json"),
		serversPath: filepath.Join(dir, "vpn_servers.json"),
		subsPath:    filepath.Join(dir, "subscriptions.json"),
	}
}

// MainPath returns the YAML path the store was initialised with.
func (s *Store) MainPath() string { return s.mainPath }

// TunnelsPath returns the JSON file path used for the writable tunnel list.
func (s *Store) TunnelsPath() string { return s.tunnelsPath }

// KeysDir returns the directory where uploaded SSH private keys are stored.
// Co-located with the YAML so backup is one folder.  Created on demand.
func (s *Store) KeysDir() string {
	return filepath.Join(filepath.Dir(s.mainPath), "keys")
}

// LoadAll reads the YAML, then overlays tunnels from tunnels.json when present.
//
// Resolution order for the tunnel list:
//  1. tunnels.json exists and is valid → it wins (and YAML's tunnels are ignored)
//  2. otherwise → fall back to whatever was in the YAML (may be empty)
func (s *Store) LoadAll() (*config.Config, error) {
	cfg, err := config.Load(s.mainPath)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(s.tunnelsPath)
	switch {
	case err == nil:
		var tunnels []config.TunnelCfg
		if err := json.Unmarshal(data, &tunnels); err != nil {
			return nil, fmt.Errorf("parse %s: %w", s.tunnelsPath, err)
		}
		// Re-expand ~ in identity_file just like YAML load does.
		for i := range tunnels {
			if tunnels[i].SSH.IdentityFile != "" {
				exp, err := config.ExpandHome(tunnels[i].SSH.IdentityFile)
				if err != nil {
					return nil, err
				}
				tunnels[i].SSH.IdentityFile = exp
			}
			if err := config.ValidateTunnel(tunnels[i]); err != nil {
				return nil, fmt.Errorf("tunnels.json[%d] (%q): %w", i, tunnels[i].Name, err)
			}
		}
		cfg.Tunnels = tunnels
	case errors.Is(err, os.ErrNotExist):
		// Use whatever (possibly empty) list came from YAML.
	default:
		return nil, fmt.Errorf("read %s: %w", s.tunnelsPath, err)
	}

	return cfg, nil
}

// SaveTunnels writes the given list to tunnels.json atomically (write to a
// sibling tmp file, fsync, then os.Rename).  Concurrent callers are
// serialised on s.mu.
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

	dir := filepath.Dir(s.tunnelsPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".tunnels-*.json")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename never happened.
		_ = os.Remove(tmpName)
	}()

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
	if err := os.Rename(tmpName, s.tunnelsPath); err != nil {
		return fmt.Errorf("rename %s: %w", s.tunnelsPath, err)
	}
	return nil
}

// ----- VPN Servers persistence -----

// LoadServers reads vpn_servers.json. Returns empty slice if file doesn't exist.
func (s *Store) LoadServers() ([]VPNServer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.serversPath)
	if errors.Is(err, os.ErrNotExist) {
		return []VPNServer{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.serversPath, err)
	}
	var servers []VPNServer
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.serversPath, err)
	}
	return servers, nil
}

// SaveServers writes the server list atomically to vpn_servers.json.
func (s *Store) SaveServers(servers []VPNServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if servers == nil {
		servers = []VPNServer{}
	}
	data, err := json.MarshalIndent(servers, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal servers: %w", err)
	}

	dir := filepath.Dir(s.serversPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".vpn-servers-*.json")
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
	if err := os.Rename(tmpName, s.serversPath); err != nil {
		return fmt.Errorf("rename %s: %w", s.serversPath, err)
	}
	return nil
}

// AddServer appends a new VPN server and persists the list.
func (s *Store) AddServer(srv VPNServer) (VPNServer, error) {
	servers, err := s.loadServersUnlocked()
	if err != nil {
		return VPNServer{}, err
	}
	if srv.ID == "" {
		srv.ID = randomID()
	}
	if srv.CreatedAt == "" {
		srv.CreatedAt = time.Now().Format(time.RFC3339)
	}
	servers = append(servers, srv)
	// saveServersUnlocked expects caller already holds no lock → use SaveServers indirectly
	if err := s.saveServersUnlocked(servers); err != nil {
		return VPNServer{}, err
	}
	return srv, nil
}

// DeleteServer removes a server by ID.
func (s *Store) DeleteServer(id string) error {
	servers, err := s.loadServersUnlocked()
	if err != nil {
		return err
	}
	found := false
	result := make([]VPNServer, 0, len(servers))
	for _, sv := range servers {
		if sv.ID == id {
			found = true
			continue
		}
		result = append(result, sv)
	}
	if !found {
		return fmt.Errorf("vpn server %q not found", id)
	}
	return s.saveServersUnlocked(result)
}

// loadServersUnlocked reads without acquiring the mutex (for internal compound ops).
func (s *Store) loadServersUnlocked() ([]VPNServer, error) {
	data, err := os.ReadFile(s.serversPath)
	if errors.Is(err, os.ErrNotExist) {
		return []VPNServer{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.serversPath, err)
	}
	var servers []VPNServer
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.serversPath, err)
	}
	return servers, nil
}

// saveServersUnlocked writes without acquiring the mutex.
func (s *Store) saveServersUnlocked(servers []VPNServer) error {
	if servers == nil {
		servers = []VPNServer{}
	}
	data, err := json.MarshalIndent(servers, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal servers: %w", err)
	}
	dir := filepath.Dir(s.serversPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".vpn-servers-*.json")
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
	if err := os.Rename(tmpName, s.serversPath); err != nil {
		return fmt.Errorf("rename %s: %w", s.serversPath, err)
	}
	return nil
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ----- Subscription Token persistence -----

// SubTokenPath returns the path to the subscription token file.
func (s *Store) SubTokenPath() string {
	return filepath.Join(filepath.Dir(s.mainPath), "sub_token.txt")
}

// LoadSubToken reads the subscription token; generates one if missing.
func (s *Store) LoadSubToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.SubTokenPath())
	if errors.Is(err, os.ErrNotExist) {
		// Generate a new 64-char hex token.
		tok := generateToken()
		if err := s.writeFileAtomic(s.SubTokenPath(), []byte(tok)); err != nil {
			return "", err
		}
		return tok, nil
	}
	if err != nil {
		return "", fmt.Errorf("read sub token: %w", err)
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" {
		tok = generateToken()
		if err := s.writeFileAtomic(s.SubTokenPath(), []byte(tok)); err != nil {
			return "", err
		}
	}
	return tok, nil
}

// RegenerateSubToken creates a new random token, replacing the old one.
func (s *Store) RegenerateSubToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tok := generateToken()
	if err := s.writeFileAtomic(s.SubTokenPath(), []byte(tok)); err != nil {
		return "", err
	}
	return tok, nil
}

func generateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ----- Subscription Sources persistence -----

// LoadSubscriptions reads subscriptions.json. Returns empty slice if missing.
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

// SaveSubscriptions writes the subscription source list atomically.
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

// writeFileAtomic writes data to path via tmp+rename.
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
