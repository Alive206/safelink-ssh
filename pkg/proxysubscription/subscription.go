// Package proxysubscription parses mainstream proxy subscription formats.
package proxysubscription

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	FormatAuto        = "auto"
	FormatClashYAML   = "clash-yaml"
	FormatURIList     = "uri-list"
	FormatSingBoxJSON = "sing-box-json"
)

const (
	ProtocolShadowsocks = "ss"
	ProtocolVMess       = "vmess"
	ProtocolVLESS       = "vless"
	ProtocolTrojan      = "trojan"
	ProtocolHysteria    = "hysteria"
	ProtocolHysteria2   = "hysteria2"
	ProtocolTUIC        = "tuic"
	ProtocolAnyTLS      = "anytls"
)

type ProxyNode struct {
	ID             string           `json:"id,omitempty" yaml:"id,omitempty"`
	SubscriptionID string           `json:"subscription_id,omitempty" yaml:"subscription_id,omitempty"`
	Name           string           `json:"name" yaml:"name"`
	Protocol       string           `json:"protocol" yaml:"protocol"`
	Server         string           `json:"server" yaml:"server"`
	Port           int              `json:"port" yaml:"port"`
	Method         string           `json:"method,omitempty" yaml:"method,omitempty"`
	Password       string           `json:"password,omitempty" yaml:"password,omitempty"`
	UUID           string           `json:"uuid,omitempty" yaml:"uuid,omitempty"`
	AlterID        int              `json:"alter_id,omitempty" yaml:"alter_id,omitempty"`
	Security       string           `json:"security,omitempty" yaml:"security,omitempty"`
	Flow           string           `json:"flow,omitempty" yaml:"flow,omitempty"`
	UDP            bool             `json:"udp,omitempty" yaml:"udp,omitempty"`
	TLS            *TLSOptions      `json:"tls,omitempty" yaml:"tls,omitempty"`
	Transport      TransportOptions `json:"transport,omitempty" yaml:"transport,omitempty"`
}

type TLSOptions struct {
	Enabled    bool     `json:"enabled" yaml:"enabled"`
	ServerName string   `json:"server_name,omitempty" yaml:"server_name,omitempty"`
	Insecure   bool     `json:"insecure,omitempty" yaml:"insecure,omitempty"`
	ALPN       []string `json:"alpn,omitempty" yaml:"alpn,omitempty"`
	PublicKey  string   `json:"public_key,omitempty" yaml:"public_key,omitempty"`
	ShortID    string   `json:"short_id,omitempty" yaml:"short_id,omitempty"`
}

type TransportOptions struct {
	Type    string            `json:"type,omitempty" yaml:"type,omitempty"`
	Path    string            `json:"path,omitempty" yaml:"path,omitempty"`
	Host    string            `json:"host,omitempty" yaml:"host,omitempty"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
}

type clashDocument struct {
	Proxies []clashProxy `yaml:"proxies"`
}

type clashProxy struct {
	Name       string       `yaml:"name"`
	Type       string       `yaml:"type"`
	Server     string       `yaml:"server"`
	Port       int          `yaml:"port"`
	Cipher     string       `yaml:"cipher"`
	Password   string       `yaml:"password"`
	UUID       string       `yaml:"uuid"`
	AlterID    int          `yaml:"alterId"`
	Security   string       `yaml:"security"`
	TLS        bool         `yaml:"tls"`
	ServerName string       `yaml:"servername"`
	SNI        string       `yaml:"sni"`
	Network    string       `yaml:"network"`
	Flow       string       `yaml:"flow"`
	UDP        bool         `yaml:"udp"`
	WSOpts     clashWSOpts  `yaml:"ws-opts"`
	Reality    clashReality `yaml:"reality-opts"`
	ALPN       stringList   `yaml:"alpn"`
	AuthStr    string       `yaml:"auth-str"`
	Up         string       `yaml:"up"`
	Down       string       `yaml:"down"`
}

type clashWSOpts struct {
	Path    string            `yaml:"path"`
	Headers map[string]string `yaml:"headers"`
}

type clashReality struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id"`
}

type stringList []string

func (s *stringList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.SequenceNode:
		for _, item := range value.Content {
			*s = append(*s, item.Value)
		}
	case yaml.ScalarNode:
		if value.Value != "" {
			*s = strings.Split(value.Value, ",")
			for i := range *s {
				(*s)[i] = strings.TrimSpace((*s)[i])
			}
		}
	}
	return nil
}

type singBoxDocument struct {
	Outbounds []singBoxOutbound `json:"outbounds"`
}

type singBoxOutbound struct {
	Type       string           `json:"type"`
	Tag        string           `json:"tag"`
	Server     string           `json:"server"`
	ServerPort int              `json:"server_port"`
	Method     string           `json:"method"`
	Password   string           `json:"password"`
	UUID       string           `json:"uuid"`
	AlterID    int              `json:"alter_id"`
	Security   string           `json:"security"`
	Flow       string           `json:"flow"`
	UDP        bool             `json:"udp"`
	TLS        *TLSOptions      `json:"tls"`
	Transport  TransportOptions `json:"transport"`
}

func Parse(data []byte, format string) ([]ProxyNode, string, error) {
	switch normalizeFormat(format) {
	case FormatClashYAML:
		nodes, err := parseClashYAML(data)
		return nodes, FormatClashYAML, err
	case FormatURIList:
		nodes, err := parseURIList(data)
		return nodes, FormatURIList, err
	case FormatSingBoxJSON:
		nodes, err := parseSingBoxJSON(data)
		return nodes, FormatSingBoxJSON, err
	case FormatAuto:
		if nodes, err := parseSingBoxJSON(data); err == nil {
			return nodes, FormatSingBoxJSON, nil
		}
		if nodes, err := parseClashYAML(data); err == nil {
			return nodes, FormatClashYAML, nil
		}
		if nodes, err := parseURIList(data); err == nil {
			return nodes, FormatURIList, nil
		}
		return nil, "", errors.New("proxy subscription is not Clash YAML, URI list or sing-box JSON")
	default:
		return nil, "", fmt.Errorf("unsupported proxy subscription format %q", format)
	}
}

func parseClashYAML(data []byte) ([]ProxyNode, error) {
	var doc clashDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Proxies) == 0 {
		return nil, errors.New("Clash YAML contains no proxies")
	}

	var nodes []ProxyNode
	for _, proxy := range doc.Proxies {
		node, ok := clashProxyToNode(proxy)
		if !ok {
			continue
		}
		if err := validateNode(node); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return nil, errors.New("Clash YAML contains no supported proxy nodes")
	}
	return nodes, nil
}

func clashProxyToNode(proxy clashProxy) (ProxyNode, bool) {
	protocol := normalizeProtocol(proxy.Type)
	if !isSupportedProtocol(protocol) {
		return ProxyNode{}, false
	}
	node := ProxyNode{
		Name:     firstNonEmpty(proxy.Name, proxy.Server),
		Protocol: protocol,
		Server:   proxy.Server,
		Port:     proxy.Port,
		Method:   proxy.Cipher,
		Password: firstNonEmpty(proxy.Password, proxy.AuthStr),
		UUID:     proxy.UUID,
		AlterID:  proxy.AlterID,
		Security: proxy.Security,
		Flow:     proxy.Flow,
		UDP:      proxy.UDP,
	}
	if protocol == ProtocolHysteria2 && node.Password == "" {
		node.Password = proxy.AuthStr
	}
	if proxy.TLS || proxy.SNI != "" || proxy.ServerName != "" || proxy.Reality.PublicKey != "" {
		node.TLS = &TLSOptions{
			Enabled:    true,
			ServerName: firstNonEmpty(proxy.ServerName, proxy.SNI),
			ALPN:       []string(proxy.ALPN),
			PublicKey:  proxy.Reality.PublicKey,
			ShortID:    proxy.Reality.ShortID,
		}
	}
	if proxy.Network != "" {
		node.Transport.Type = proxy.Network
	}
	if proxy.WSOpts.Path != "" || len(proxy.WSOpts.Headers) > 0 {
		node.Transport.Type = firstNonEmpty(node.Transport.Type, "ws")
		node.Transport.Path = proxy.WSOpts.Path
		node.Transport.Headers = proxy.WSOpts.Headers
		node.Transport.Host = proxy.WSOpts.Headers["Host"]
	}
	return node, true
}

func parseSingBoxJSON(data []byte) ([]ProxyNode, error) {
	var doc singBoxDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Outbounds) == 0 {
		return nil, errors.New("sing-box JSON contains no outbounds")
	}
	var nodes []ProxyNode
	for _, outbound := range doc.Outbounds {
		protocol := normalizeProtocol(outbound.Type)
		if !isSupportedProtocol(protocol) {
			continue
		}
		node := ProxyNode{
			Name:      firstNonEmpty(outbound.Tag, outbound.Server),
			Protocol:  protocol,
			Server:    outbound.Server,
			Port:      outbound.ServerPort,
			Method:    outbound.Method,
			Password:  outbound.Password,
			UUID:      outbound.UUID,
			AlterID:   outbound.AlterID,
			Security:  outbound.Security,
			Flow:      outbound.Flow,
			UDP:       outbound.UDP,
			TLS:       outbound.TLS,
			Transport: outbound.Transport,
		}
		if err := validateNode(node); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return nil, errors.New("sing-box JSON contains no supported proxy outbounds")
	}
	return nodes, nil
}

func parseURIList(data []byte) ([]ProxyNode, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, errors.New("URI list is empty")
	}
	if !looksLikeURIList(text) {
		if decoded, ok := decodeBase64Text(text); ok {
			text = decoded
		}
	}
	lines := splitURIItems(text)
	var nodes []ProxyNode
	for _, line := range lines {
		node, err := parseURI(line)
		if err != nil {
			continue
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return nil, errors.New("URI list contains no supported proxy nodes")
	}
	return nodes, nil
}

func parseURI(raw string) (ProxyNode, error) {
	switch {
	case strings.HasPrefix(raw, "ss://"):
		return parseShadowsocksURI(raw)
	case strings.HasPrefix(raw, "vmess://"):
		return parseVMessURI(raw)
	case strings.HasPrefix(raw, "vless://"):
		return parseUserInfoURI(raw, ProtocolVLESS)
	case strings.HasPrefix(raw, "trojan://"):
		return parseUserInfoURI(raw, ProtocolTrojan)
	case strings.HasPrefix(raw, "hysteria2://"), strings.HasPrefix(raw, "hy2://"):
		raw = strings.Replace(raw, "hy2://", "hysteria2://", 1)
		return parseUserInfoURI(raw, ProtocolHysteria2)
	case strings.HasPrefix(raw, "tuic://"):
		return parseUserInfoURI(raw, ProtocolTUIC)
	case strings.HasPrefix(raw, "anytls://"):
		return parseUserInfoURI(raw, ProtocolAnyTLS)
	default:
		return ProxyNode{}, fmt.Errorf("unsupported proxy URI %q", raw)
	}
}

func parseShadowsocksURI(raw string) (ProxyNode, error) {
	name := fragmentName(raw)
	noFragment := stripFragment(raw)
	body := strings.TrimPrefix(noFragment, "ss://")
	query := ""
	if idx := strings.Index(body, "?"); idx >= 0 {
		query = body[idx:]
		body = body[:idx]
	}
	if !strings.Contains(body, "@") {
		decoded, ok := decodeBase64Text(body)
		if !ok {
			return ProxyNode{}, fmt.Errorf("invalid shadowsocks URI credentials")
		}
		body = decoded
	}
	u, err := url.Parse("ss://" + body + query)
	if err != nil {
		return ProxyNode{}, err
	}
	port, err := parsePort(u.Port())
	if err != nil {
		return ProxyNode{}, err
	}
	method := u.User.Username()
	password, _ := u.User.Password()
	node := ProxyNode{
		Name:     firstNonEmpty(name, u.Hostname()),
		Protocol: ProtocolShadowsocks,
		Server:   u.Hostname(),
		Port:     port,
		Method:   method,
		Password: password,
	}
	return node, validateNode(node)
}

func parseVMessURI(raw string) (ProxyNode, error) {
	payload := strings.TrimPrefix(stripFragment(raw), "vmess://")
	decoded, ok := decodeBase64Text(payload)
	if !ok {
		return ProxyNode{}, fmt.Errorf("invalid vmess URI payload")
	}
	var doc struct {
		Name     string `json:"ps"`
		Server   string `json:"add"`
		Port     string `json:"port"`
		UUID     string `json:"id"`
		AlterID  string `json:"aid"`
		Network  string `json:"net"`
		Security string `json:"scy"`
		Host     string `json:"host"`
		Path     string `json:"path"`
		TLS      string `json:"tls"`
		SNI      string `json:"sni"`
		Type     string `json:"type"`
	}
	if err := json.Unmarshal([]byte(decoded), &doc); err != nil {
		return ProxyNode{}, err
	}
	port, err := parsePort(doc.Port)
	if err != nil {
		return ProxyNode{}, err
	}
	alterID, _ := strconv.Atoi(doc.AlterID)
	node := ProxyNode{
		Name:      firstNonEmpty(doc.Name, doc.Server),
		Protocol:  ProtocolVMess,
		Server:    doc.Server,
		Port:      port,
		UUID:      doc.UUID,
		AlterID:   alterID,
		Security:  doc.Security,
		Transport: TransportOptions{Type: doc.Network, Path: doc.Path, Host: doc.Host},
	}
	if doc.TLS == "tls" || doc.SNI != "" {
		node.TLS = &TLSOptions{Enabled: true, ServerName: firstNonEmpty(doc.SNI, doc.Host)}
	}
	return node, validateNode(node)
}

func parseUserInfoURI(raw, protocol string) (ProxyNode, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return ProxyNode{}, err
	}
	port, err := parsePort(u.Port())
	if err != nil {
		return ProxyNode{}, err
	}
	q := u.Query()
	secret := u.User.Username()
	password, hasPassword := u.User.Password()
	node := ProxyNode{
		Name:      firstNonEmpty(fragmentName(raw), u.Hostname()),
		Protocol:  protocol,
		Server:    u.Hostname(),
		Port:      port,
		Flow:      q.Get("flow"),
		Security:  q.Get("security"),
		Transport: TransportOptions{Type: q.Get("type"), Path: q.Get("path"), Host: firstNonEmpty(q.Get("host"), q.Get("Host"))},
	}
	switch protocol {
	case ProtocolVLESS:
		node.UUID = secret
	case ProtocolTUIC:
		node.UUID = secret
		if hasPassword {
			node.Password = password
		} else {
			node.Password = q.Get("password")
		}
	default:
		node.Password = secret
	}
	if shouldEnableTLS(q) {
		node.TLS = &TLSOptions{
			Enabled:    true,
			ServerName: firstNonEmpty(q.Get("sni"), q.Get("peer")),
			Insecure:   tlsInsecureFromQuery(q),
			PublicKey:  q.Get("pbk"),
			ShortID:    q.Get("sid"),
		}
	}
	return node, validateNode(node)
}

func shouldEnableTLS(q url.Values) bool {
	return q.Get("security") == "tls" ||
		q.Get("security") == "reality" ||
		q.Get("sni") != "" ||
		q.Get("peer") != ""
}

func tlsInsecureFromQuery(q url.Values) bool {
	switch q.Get("allowInsecure") {
	case "1", "true":
		return true
	}
	switch q.Get("insecure") {
	case "1", "true":
		return true
	}
	return q.Get("skip-cert-verify") == "true"
}

func validateNode(node ProxyNode) error {
	if strings.TrimSpace(node.Name) == "" {
		return errors.New("proxy node requires name")
	}
	if strings.TrimSpace(node.Server) == "" {
		return fmt.Errorf("%s: proxy node requires server", node.Name)
	}
	if node.Port <= 0 || node.Port > 65535 {
		return fmt.Errorf("%s: proxy node requires valid port", node.Name)
	}
	switch node.Protocol {
	case ProtocolShadowsocks:
		if node.Method == "" || node.Password == "" {
			return fmt.Errorf("%s: shadowsocks node requires method and password", node.Name)
		}
	case ProtocolVMess, ProtocolVLESS:
		if node.UUID == "" {
			return fmt.Errorf("%s: %s node requires uuid", node.Name, node.Protocol)
		}
	case ProtocolTrojan, ProtocolHysteria, ProtocolHysteria2, ProtocolAnyTLS:
		if node.Password == "" {
			return fmt.Errorf("%s: %s node requires password", node.Name, node.Protocol)
		}
	case ProtocolTUIC:
		if node.UUID == "" || node.Password == "" {
			return fmt.Errorf("%s: tuic node requires uuid and password", node.Name)
		}
	default:
		return fmt.Errorf("%s: unsupported proxy protocol %q", node.Name, node.Protocol)
	}
	return nil
}

func splitURIItems(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func looksLikeURIList(text string) bool {
	for _, prefix := range []string{"ss://", "vmess://", "vless://", "trojan://", "hysteria2://", "hy2://", "tuic://", "anytls://"} {
		if strings.Contains(text, prefix) {
			return true
		}
	}
	return false
}

func decodeBase64Text(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	padding := len(trimmed) % 4
	if padding != 0 {
		trimmed += strings.Repeat("=", 4-padding)
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(trimmed); err == nil {
			return string(decoded), true
		}
	}
	return "", false
}

func stripFragment(raw string) string {
	if idx := strings.Index(raw, "#"); idx >= 0 {
		return raw[:idx]
	}
	return raw
}

func fragmentName(raw string) string {
	if idx := strings.Index(raw, "#"); idx >= 0 {
		if name, err := url.QueryUnescape(raw[idx+1:]); err == nil {
			return name
		}
		return raw[idx+1:]
	}
	return ""
}

func parsePort(text string) (int, error) {
	port, err := strconv.Atoi(text)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", text)
	}
	return port, nil
}

func normalizeProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "ss", "shadowsocks":
		return ProtocolShadowsocks
	case "vmess":
		return ProtocolVMess
	case "vless":
		return ProtocolVLESS
	case "trojan":
		return ProtocolTrojan
	case "hysteria":
		return ProtocolHysteria
	case "hysteria2", "hy2":
		return ProtocolHysteria2
	case "tuic":
		return ProtocolTUIC
	case "anytls":
		return ProtocolAnyTLS
	default:
		return strings.ToLower(strings.TrimSpace(protocol))
	}
}

func isSupportedProtocol(protocol string) bool {
	switch protocol {
	case ProtocolShadowsocks, ProtocolVMess, ProtocolVLESS, ProtocolTrojan, ProtocolHysteria, ProtocolHysteria2, ProtocolTUIC, ProtocolAnyTLS:
		return true
	default:
		return false
	}
}

func normalizeFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "", "auto":
		return FormatAuto
	case "clash", "yaml", "yml", "clash-yaml":
		return FormatClashYAML
	case "uri", "uri-list", "v2ray", "base64":
		return FormatURIList
	case "sing-box", "singbox", "json", "sing-box-json":
		return FormatSingBoxJSON
	default:
		return format
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func splitHostPort(hostport string) (string, int, error) {
	host, portText, err := net.SplitHostPort(hostport)
	if err != nil {
		return "", 0, err
	}
	port, err := parsePort(portText)
	return host, port, err
}
