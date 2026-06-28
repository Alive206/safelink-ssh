package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ServerCfg describes the SafeLink server process configuration.
type ServerCfg struct {
	Listen string `yaml:"listen" json:"listen"`
	Subnet string `yaml:"subnet" json:"subnet"`
	Web    WebCfg `yaml:"web" json:"web"`
	TLS    TLSCfg `yaml:"tls" json:"tls"`
	NAT    NATCfg `yaml:"nat" json:"nat"`
}

// TLSCfg configures the QUIC server certificate.
type TLSCfg struct {
	Cert string `yaml:"cert" json:"cert"`
	Key  string `yaml:"key" json:"key"`
}

// NATCfg configures server-side forwarding/NAT.
type NATCfg struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Iface   string `yaml:"iface" json:"iface"`
}

// DefaultServerConfig returns the same defaults used by the CLI flags.
func DefaultServerConfig() ServerCfg {
	return ServerCfg{
		Listen: ":1562",
		Subnet: "10.0.8.0/24",
		Web: WebCfg{
			Addr: "0.0.0.0:8080",
		},
	}
}

// LoadServerConfig reads a YAML server configuration file. Missing files use defaults.
func LoadServerConfig(path string) (ServerCfg, error) {
	cfg := DefaultServerConfig()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return ServerCfg{}, fmt.Errorf("read server config: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ServerCfg{}, fmt.Errorf("parse server config: %w", err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *ServerCfg) applyDefaults() {
	defaults := DefaultServerConfig()
	if c.Listen == "" {
		c.Listen = defaults.Listen
	}
	if c.Subnet == "" {
		c.Subnet = defaults.Subnet
	}
	if c.Web.Addr == "" {
		c.Web.Addr = defaults.Web.Addr
	}
}
