// Package config defines the YAML schema for sshtunneld and provides loading
// utilities including environment-variable interpolation and home-dir expansion.
package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration loaded from YAML.
//
// Phase-2 introduces a `web:` section and externalises the tunnel list to a
// dedicated JSON store (configs/tunnels.json) that the web UI may safely
// rewrite.  The YAML may still embed `tunnels:` for backwards compatibility;
// the store layer decides which source wins.
type Config struct {
	LogLevel        string       `yaml:"log_level" json:"log_level"`
	KnownHosts      string       `yaml:"known_hosts" json:"known_hosts"`
	InsecureHostKey bool         `yaml:"insecure_host_key" json:"insecure_host_key"`
	Defaults        ConnDefaults `yaml:"defaults" json:"defaults"`
	Web             WebCfg       `yaml:"web" json:"web"`
	Tunnels         []TunnelCfg  `yaml:"tunnels" json:"tunnels"`
}

// ConnDefaults holds shared connection-level defaults.
type ConnDefaults struct {
	KeepAliveInterval time.Duration `yaml:"keepalive_interval" json:"keepalive_interval"`
	KeepAliveMaxFails int           `yaml:"keepalive_max_fails" json:"keepalive_max_fails"`
	DialTimeout       time.Duration `yaml:"dial_timeout" json:"dial_timeout"`
	ReconnectInitial  time.Duration `yaml:"reconnect_initial" json:"reconnect_initial"`
	ReconnectMax      time.Duration `yaml:"reconnect_max" json:"reconnect_max"`
}

// WebCfg configures the embedded HTTP control panel.  When Addr is empty the
// web panel is disabled entirely.
type WebCfg struct {
	Addr string  `yaml:"addr" json:"addr"`
	Auth AuthCfg `yaml:"auth" json:"auth"`
}

// AuthCfg configures authentication for the web control panel.
//
//   - APIToken — static bearer token (machine-to-machine).  Empty disables it.
//   - Users    — bcrypt-hashed credentials accepted by /api/login.
type AuthCfg struct {
	APIToken string    `yaml:"api_token" json:"api_token"`
	Users    []UserCfg `yaml:"users" json:"users"`
}

// UserCfg is a single user/bcrypt-hash pair for the web panel.
type UserCfg struct {
	Username     string `yaml:"username" json:"username"`
	PasswordHash string `yaml:"password_hash" json:"password_hash"`
}

// TunnelCfg describes a single tunnel.
//
//	mode=local   listen on local addr, dial forward via SSH
//	mode=remote  ask remote sshd to listen, dial forward locally
//	mode=dynamic local SOCKS5 server, dial via SSH
type TunnelCfg struct {
	Name    string `yaml:"name" json:"name"`
	Mode    string `yaml:"mode" json:"mode"`
	SSH     SSHCfg `yaml:"ssh" json:"ssh"`
	Listen  string `yaml:"listen" json:"listen"`
	Forward string `yaml:"forward" json:"forward"`
}

// SSHCfg describes how to authenticate to the SSH server.
// IdentityFile and Password may be combined; the publickey method is offered
// first when both are present.
type SSHCfg struct {
	Addr         string `yaml:"addr" json:"addr"`
	User         string `yaml:"user" json:"user"`
	IdentityFile string `yaml:"identity_file" json:"identity_file"`
	Passphrase   string `yaml:"passphrase" json:"passphrase"`
	Password     string `yaml:"password" json:"password"`
}

// Tunnel modes.
const (
	ModeLocal   = "local"
	ModeRemote  = "remote"
	ModeDynamic = "dynamic"
)

// Load reads and parses the YAML config from path, expands ${ENV} references
// and ~ paths, applies defaults and validates the result.
//
// Note: phase-2 tolerates an empty `tunnels:` list because tunnels may live in
// configs/tunnels.json.  The store layer is responsible for ensuring at least
// one tunnel is reachable before runtime.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	// Expand only ${VAR} (with explicit braces) so secret material that
	// happens to contain '$' — most notably bcrypt password hashes like
	// "$2a$10$..." — survives unchanged.  Use os.ExpandEnv-style behaviour
	// (empty for unset vars) on a per-match basis.
	expanded := braceVar.ReplaceAllStringFunc(string(raw), func(s string) string {
		name := s[2 : len(s)-1]
		return os.Getenv(name)
	})

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if err := cfg.applyDefaultsAndExpand(); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ValidateTunnel runs the per-tunnel sanity checks used by both startup and
// the web API's create/update handlers.
func ValidateTunnel(t TunnelCfg) error { return validateTunnel(t) }

// braceVar matches ${IDENT} placeholders for environment-variable expansion.
var braceVar = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)
