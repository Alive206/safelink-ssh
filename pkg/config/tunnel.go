// Package config defines shared configuration structures used by both
// the SafeLink server and client modules.
package config

import (
	"time"
)

// Tunnel modes.
const (
	ModeLocal   = "local"
	ModeRemote  = "remote"
	ModeDynamic = "dynamic"
	ModeVPN     = "vpn"
)

// TunnelCfg describes a single tunnel.
type TunnelCfg struct {
	Name    string `yaml:"name" json:"name"`
	Mode    string `yaml:"mode" json:"mode"`
	SSH     SSHCfg `yaml:"ssh" json:"ssh"`
	Listen  string `yaml:"listen" json:"listen"`
	Forward string `yaml:"forward" json:"forward"`

	// Transport wraps the SSH TCP connection before the SSH handshake.
	Transport string `yaml:"transport" json:"transport"`

	// Tun is required when Mode is "vpn".
	Tun TunCfg `yaml:"tun" json:"tun"`
}

// TunCfg configures the TUN virtual network interface for VPN mode.
type TunCfg struct {
	Subnet    string   `yaml:"subnet" json:"subnet"`
	DNS       []string `yaml:"dns" json:"dns"`
	AutoRoute bool     `yaml:"auto_route" json:"auto_route"`
	TLSCert   string   `yaml:"tls_cert" json:"tls_cert"`
	TLSKey    string   `yaml:"tls_key" json:"tls_key"`
	SNI       string   `yaml:"sni" json:"sni"`
	PinSHA256 string   `yaml:"pin_sha256" json:"pin_sha256"`
	Padding   *bool    `yaml:"padding" json:"padding"`
}

// SSHCfg describes how to authenticate to the SSH server.
type SSHCfg struct {
	Addr         string `yaml:"addr" json:"addr"`
	User         string `yaml:"user" json:"user"`
	IdentityFile string `yaml:"identity_file" json:"identity_file"`
	Passphrase   string `yaml:"passphrase" json:"passphrase"`
	Password     string `yaml:"password" json:"password"`
}

// ConnDefaults holds shared connection-level defaults.
type ConnDefaults struct {
	KeepAliveInterval time.Duration `yaml:"keepalive_interval" json:"keepalive_interval"`
	KeepAliveMaxFails int           `yaml:"keepalive_max_fails" json:"keepalive_max_fails"`
	DialTimeout       time.Duration `yaml:"dial_timeout" json:"dial_timeout"`
	ReconnectInitial  time.Duration `yaml:"reconnect_initial" json:"reconnect_initial"`
	ReconnectMax      time.Duration `yaml:"reconnect_max" json:"reconnect_max"`
}

// AuthCfg configures authentication for the web control panel.
type AuthCfg struct {
	APIToken string    `yaml:"api_token" json:"api_token"`
	Users    []UserCfg `yaml:"users" json:"users"`
}

// UserCfg is a single user/bcrypt-hash pair.
type UserCfg struct {
	Username     string `yaml:"username" json:"username"`
	PasswordHash string `yaml:"password_hash" json:"password_hash"`
}

// WebCfg configures the embedded HTTP control panel.
type WebCfg struct {
	Addr string  `yaml:"addr" json:"addr"`
	Auth AuthCfg `yaml:"auth" json:"auth"`
}
