package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/example/sshtunneld/internal/config"

	"gopkg.in/yaml.v3"
)

// clashProxy is the subset of Clash proxy fields we support.
type clashProxy struct {
	Name     string `yaml:"name" json:"name"`
	Type     string `yaml:"type" json:"type"`
	Server   string `yaml:"server" json:"server"`
	Port     int    `yaml:"port" json:"port"`
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
	Password string `yaml:"password,omitempty" json:"password,omitempty"`
}

type clashDoc struct {
	Proxies []clashProxy `yaml:"proxies" json:"proxies"`
}

// writeClashYAML serialises tunnels to Clash-compatible YAML.
// Mapping:
//   dynamic → socks5 (listen address)
//   vpn     → socks5 (forward/server address as proxy entry)
//   local   → socks5 (listen address)
func writeClashYAML(w io.Writer, tunnels []config.TunnelCfg) {
	var proxies []clashProxy
	for _, t := range tunnels {
		switch t.Mode {
		case config.ModeDynamic:
			host, portStr, err := net.SplitHostPort(t.Listen)
			if err != nil {
				continue
			}
			port, _ := strconv.Atoi(portStr)
			if host == "" || host == "0.0.0.0" {
				host = "127.0.0.1"
			}
			proxies = append(proxies, clashProxy{
				Name:   t.Name,
				Type:   "socks5",
				Server: host,
				Port:   port,
			})
		case config.ModeVPN:
			// VPN mode: use the forward address (VPN server) as a socks5 entry.
			addr := t.Forward
			if addr == "" {
				addr = t.SSH.Addr
			}
			if addr == "" {
				continue
			}
			host, portStr, err := net.SplitHostPort(addr)
			if err != nil {
				// Might be just an IP without port
				host = addr
				portStr = "1080"
			}
			port, _ := strconv.Atoi(portStr)
			proxies = append(proxies, clashProxy{
				Name:     t.Name,
				Type:     "socks5",
				Server:   host,
				Port:     port,
				Username: t.SSH.User,
				Password: t.SSH.Password,
			})
		case config.ModeLocal:
			host, portStr, err := net.SplitHostPort(t.Listen)
			if err != nil {
				continue
			}
			port, _ := strconv.Atoi(portStr)
			if host == "" || host == "0.0.0.0" {
				host = "127.0.0.1"
			}
			proxies = append(proxies, clashProxy{
				Name:   t.Name,
				Type:   "socks5",
				Server: host,
				Port:   port,
			})
		// remote mode skipped — doesn't make sense as a Clash proxy
		}
	}
	if proxies == nil {
		proxies = []clashProxy{}
	}
	doc := clashDoc{Proxies: proxies}
	_ = yaml.NewEncoder(w).Encode(doc)
}

// parseSubscription auto-detects format and returns parsed tunnels.
// hint can be "json", "clash", or "auto" (empty = auto).
func parseSubscription(data []byte, hint string) ([]config.TunnelCfg, error) {
	if hint == "" {
		hint = "auto"
	}

	// Try SafeLink JSON first.
	if hint == "json" || hint == "auto" {
		var tunnels []config.TunnelCfg
		if err := json.Unmarshal(data, &tunnels); err == nil && len(tunnels) > 0 {
			// Basic sanity: check first entry has a name.
			if tunnels[0].Name != "" {
				return tunnels, nil
			}
		}
	}

	// Try Clash YAML.
	if hint == "clash" || hint == "auto" {
		tunnels, err := parseClashYAML(data)
		if err == nil && len(tunnels) > 0 {
			return tunnels, nil
		}
	}

	return nil, fmt.Errorf("unable to parse subscription content")
}

// parseClashYAML extracts socks5 proxies from Clash YAML and maps them to TunnelCfg.
func parseClashYAML(data []byte) ([]config.TunnelCfg, error) {
	var doc clashDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	var result []config.TunnelCfg
	for _, p := range doc.Proxies {
		switch p.Type {
		case "socks5":
			result = append(result, config.TunnelCfg{
				Name: p.Name,
				Mode: config.ModeDynamic,
				SSH: config.SSHCfg{
					Addr: fmt.Sprintf("%s:%d", p.Server, p.Port),
				},
				Listen: "127.0.0.1:0",
			})
		// Other types (ss, vmess, etc.) not supported yet — skip.
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no supported proxies found in Clash YAML")
	}
	return result, nil
}
