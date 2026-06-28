// Package subscription encodes and parses SafeLink VPN subscription documents.
package subscription

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/example/safelink/pkg/config"
	"gopkg.in/yaml.v3"
)

const (
	FormatAuto         = "auto"
	FormatSafeLinkJSON = "safelink-json"
	FormatClashYAML    = "clash-yaml"
)

const clashSafeLinkType = "safelink-vpn"

type Document struct {
	Version int                `json:"version" yaml:"version"`
	Tunnels []config.TunnelCfg `json:"tunnels" yaml:"tunnels"`
}

type clashDocument struct {
	Proxies []clashProxy `yaml:"proxies"`
}

type clashProxy struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`
	Server    string   `yaml:"server"`
	Port      int      `yaml:"port"`
	Username  string   `yaml:"username"`
	Password  string   `yaml:"password"`
	SNI       string   `yaml:"sni,omitempty"`
	Subnet    string   `yaml:"subnet,omitempty"`
	DNS       []string `yaml:"dns,omitempty"`
	AutoRoute bool     `yaml:"auto-route,omitempty"`
	PinSHA256 string   `yaml:"pin-sha256,omitempty"`
	Padding   *bool    `yaml:"padding,omitempty"`
}

func Parse(data []byte, format string) ([]config.TunnelCfg, string, error) {
	switch normalizeFormat(format) {
	case FormatSafeLinkJSON:
		tunnels, err := parseSafeLinkJSON(data)
		return tunnels, FormatSafeLinkJSON, err
	case FormatClashYAML:
		tunnels, err := parseClashYAML(data)
		return tunnels, FormatClashYAML, err
	case FormatAuto:
		if tunnels, err := parseSafeLinkJSON(data); err == nil {
			return tunnels, FormatSafeLinkJSON, nil
		}
		if tunnels, err := parseClashYAML(data); err == nil {
			return tunnels, FormatClashYAML, nil
		}
		return nil, "", errors.New("subscription is neither SafeLink JSON nor supported Clash YAML")
	default:
		return nil, "", fmt.Errorf("unsupported subscription format %q", format)
	}
}

func EncodeSafeLinkJSON(tunnels []config.TunnelCfg) ([]byte, error) {
	return json.MarshalIndent(Document{Version: 1, Tunnels: onlyVPN(tunnels)}, "", "  ")
}

func EncodeClashYAML(tunnels []config.TunnelCfg) ([]byte, error) {
	doc := clashDocument{Proxies: make([]clashProxy, 0, len(tunnels))}
	for _, tunnel := range onlyVPN(tunnels) {
		host, port, err := splitHostPort(tunnel.Forward)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", tunnel.Name, err)
		}
		doc.Proxies = append(doc.Proxies, clashProxy{
			Name:      tunnel.Name,
			Type:      clashSafeLinkType,
			Server:    host,
			Port:      port,
			Username:  tunnel.SSH.User,
			Password:  tunnel.SSH.Password,
			SNI:       tunnel.Tun.SNI,
			Subnet:    tunnel.Tun.Subnet,
			DNS:       tunnel.Tun.DNS,
			AutoRoute: tunnel.Tun.AutoRoute,
			PinSHA256: tunnel.Tun.PinSHA256,
			Padding:   tunnel.Tun.Padding,
		})
	}
	return yaml.Marshal(doc)
}

func parseSafeLinkJSON(data []byte) ([]config.TunnelCfg, error) {
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Tunnels) == 0 {
		return nil, errors.New("SafeLink JSON contains no tunnels")
	}
	return normalizeTunnels(doc.Tunnels)
}

func parseClashYAML(data []byte) ([]config.TunnelCfg, error) {
	var doc clashDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Proxies) == 0 {
		return nil, errors.New("Clash YAML contains no proxies")
	}

	tunnels := make([]config.TunnelCfg, 0, len(doc.Proxies))
	for _, proxy := range doc.Proxies {
		if proxy.Type != clashSafeLinkType {
			continue
		}
		if proxy.Name == "" || proxy.Server == "" || proxy.Port == 0 {
			return nil, fmt.Errorf("safelink-vpn proxy %q requires name, server and port", proxy.Name)
		}
		tunnels = append(tunnels, config.TunnelCfg{
			Name:    proxy.Name,
			Mode:    config.ModeVPN,
			Forward: net.JoinHostPort(proxy.Server, strconv.Itoa(proxy.Port)),
			SSH: config.SSHCfg{
				User:     proxy.Username,
				Password: proxy.Password,
			},
			Tun: config.TunCfg{
				Subnet:    proxy.Subnet,
				DNS:       proxy.DNS,
				AutoRoute: proxy.AutoRoute,
				SNI:       proxy.SNI,
				PinSHA256: proxy.PinSHA256,
				Padding:   proxy.Padding,
			},
		})
	}
	if len(tunnels) == 0 {
		return nil, errors.New("Clash YAML contains no safelink-vpn proxies")
	}
	return normalizeTunnels(tunnels)
}

func normalizeTunnels(tunnels []config.TunnelCfg) ([]config.TunnelCfg, error) {
	out := make([]config.TunnelCfg, 0, len(tunnels))
	for _, tunnel := range tunnels {
		if tunnel.Mode == "" {
			tunnel.Mode = config.ModeVPN
		}
		if tunnel.Mode != config.ModeVPN {
			continue
		}
		if err := config.ValidateTunnel(tunnel); err != nil {
			return nil, fmt.Errorf("%s: %w", tunnel.Name, err)
		}
		out = append(out, tunnel)
	}
	if len(out) == 0 {
		return nil, errors.New("subscription contains no VPN tunnels")
	}
	return out, nil
}

func onlyVPN(tunnels []config.TunnelCfg) []config.TunnelCfg {
	out := make([]config.TunnelCfg, 0, len(tunnels))
	for _, tunnel := range tunnels {
		if tunnel.Mode == config.ModeVPN {
			out = append(out, tunnel)
		}
	}
	return out
}

func normalizeFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return FormatAuto
	}
	if format == "json" {
		return FormatSafeLinkJSON
	}
	if format == "clash" || format == "yaml" || format == "yml" {
		return FormatClashYAML
	}
	return format
}

func splitHostPort(addr string) (string, int, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid forward address %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port %q", portText)
	}
	return host, port, nil
}
