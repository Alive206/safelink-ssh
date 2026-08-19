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

// vmess/vless 的 UDP 封装方式。sing-box 只认 xudp 和 packetaddr，写其他值会直接启动失败，
// 所以订阅里的写法得先归一。PacketEncodingDisabled 是节点显式要求不开的内部标记。
const (
	PacketEncodingXUDP       = "xudp"
	PacketEncodingPacketAddr = "packetaddr"
	PacketEncodingDisabled   = "none"
)

// NormalizePacketEncoding 把订阅里的各种写法收拢成 sing-box 能接受的取值，
// 认不出来的值当成“没指定”，留给上层决定默认值。
func NormalizePacketEncoding(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case PacketEncodingXUDP:
		return PacketEncodingXUDP
	case PacketEncodingPacketAddr, "packet-addr", "packet_addr":
		return PacketEncodingPacketAddr
	case PacketEncodingDisabled, "disabled", "false":
		return PacketEncodingDisabled
	default:
		return ""
	}
}

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
	PacketEncoding string           `json:"packet_encoding,omitempty" yaml:"packet_encoding,omitempty"`
	TLS            *TLSOptions      `json:"tls,omitempty" yaml:"tls,omitempty"`
	Transport      TransportOptions `json:"transport,omitempty" yaml:"transport,omitempty"`
}

type TLSOptions struct {
	Enabled     bool     `json:"enabled" yaml:"enabled"`
	ServerName  string   `json:"server_name,omitempty" yaml:"server_name,omitempty"`
	Insecure    bool     `json:"insecure,omitempty" yaml:"insecure,omitempty"`
	ALPN        []string `json:"alpn,omitempty" yaml:"alpn,omitempty"`
	PublicKey   string   `json:"public_key,omitempty" yaml:"public_key,omitempty"`
	ShortID     string   `json:"short_id,omitempty" yaml:"short_id,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty" yaml:"fingerprint,omitempty"`
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
	Name              string        `yaml:"name"`
	Type              string        `yaml:"type"`
	Server            string        `yaml:"server"`
	Port              flexibleInt   `yaml:"port"`
	Cipher            string        `yaml:"cipher"`
	Password          string        `yaml:"password"`
	UUID              string        `yaml:"uuid"`
	AlterID           flexibleInt   `yaml:"alterId"`
	Security          string        `yaml:"security"`
	TLS               bool          `yaml:"tls"`
	ServerName        string        `yaml:"servername"`
	SNI               string        `yaml:"sni"`
	Network           string        `yaml:"network"`
	Flow              string        `yaml:"flow"`
	UDP               bool          `yaml:"udp"`
	PacketEncoding    string        `yaml:"packet-encoding"`
	XUDP              bool          `yaml:"xudp"`
	SkipCertVerify    bool          `yaml:"skip-cert-verify"`
	ClientFingerprint string        `yaml:"client-fingerprint"`
	WSOpts            clashWSOpts   `yaml:"ws-opts"`
	GRPCOpts          clashGRPCOpts `yaml:"grpc-opts"`
	Reality           clashReality  `yaml:"reality-opts"`
	ALPN              stringList    `yaml:"alpn"`
	AuthStr           string        `yaml:"auth-str"`
	Up                string        `yaml:"up"`
	Down              string        `yaml:"down"`
}

type clashWSOpts struct {
	Path    string            `yaml:"path"`
	Headers map[string]string `yaml:"headers"`
}

type clashGRPCOpts struct {
	ServiceName string `yaml:"grpc-service-name"`
}

type clashReality struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id"`
}

// flexibleInt 兼容机场把 port / alterId 写成字符串的情况（port: "443"）。
// yaml.v3 遇到类型不匹配会让整份订阅解析失败，一个节点写法特殊就会导致全部节点丢失。
type flexibleInt int

func (f *flexibleInt) UnmarshalYAML(value *yaml.Node) error {
	f.parse(value.Value)
	return nil
}

func (f *flexibleInt) UnmarshalJSON(data []byte) error {
	f.parse(strings.Trim(strings.TrimSpace(string(data)), `"`))
	return nil
}

// parse 解析失败时保持 0，由 validateNode 把该节点判为无效并跳过。
func (f *flexibleInt) parse(text string) {
	if n, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
		*f = flexibleInt(n)
	}
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
	Type           string           `json:"type"`
	Tag            string           `json:"tag"`
	Server         string           `json:"server"`
	ServerPort     flexibleInt      `json:"server_port"`
	Method         string           `json:"method"`
	Password       string           `json:"password"`
	UUID           string           `json:"uuid"`
	AlterID        flexibleInt      `json:"alter_id"`
	Security       string           `json:"security"`
	Flow           string           `json:"flow"`
	UDP            bool             `json:"udp"`
	PacketEncoding string           `json:"packet_encoding"`
	TLS            *TLSOptions      `json:"tls"`
	Transport      TransportOptions `json:"transport"`
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
	var firstErr error
	for _, proxy := range doc.Proxies {
		node, ok := clashProxyToNode(proxy)
		if !ok {
			continue
		}
		if IsInformationalNodeName(node.Name) {
			continue
		}
		// 机场订阅里常有个别字段缺失的节点，跳过它们而不是丢掉整份订阅。
		if err := validateNode(node); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
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
		Port:     int(proxy.Port),
		Method:   proxy.Cipher,
		Password: firstNonEmpty(proxy.Password, proxy.AuthStr),
		UUID:     proxy.UUID,
		AlterID:  int(proxy.AlterID),
		Security: proxy.Security,
		Flow:     proxy.Flow,
		UDP:      proxy.UDP,
	}
	// mihomo 用 packet-encoding 字段，vless 还有个单独的 xudp 开关。
	node.PacketEncoding = NormalizePacketEncoding(proxy.PacketEncoding)
	if node.PacketEncoding == "" && proxy.XUDP {
		node.PacketEncoding = PacketEncodingXUDP
	}
	if protocol == ProtocolHysteria2 && node.Password == "" {
		node.Password = proxy.AuthStr
	}
	if proxy.TLS || proxy.SNI != "" || proxy.ServerName != "" || proxy.Reality.PublicKey != "" {
		node.TLS = &TLSOptions{
			Enabled:     true,
			ServerName:  firstNonEmpty(proxy.ServerName, proxy.SNI),
			Insecure:    proxy.SkipCertVerify,
			ALPN:        []string(proxy.ALPN),
			PublicKey:   proxy.Reality.PublicKey,
			ShortID:     proxy.Reality.ShortID,
			Fingerprint: proxy.ClientFingerprint,
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
	// gRPC 的 service name 在 Clash 里单独一个字段，丢了节点就连不上。
	if proxy.GRPCOpts.ServiceName != "" {
		node.Transport.Type = firstNonEmpty(node.Transport.Type, "grpc")
		node.Transport.Path = proxy.GRPCOpts.ServiceName
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
	var firstErr error
	for _, outbound := range doc.Outbounds {
		protocol := normalizeProtocol(outbound.Type)
		if !isSupportedProtocol(protocol) {
			continue
		}
		node := ProxyNode{
			Name:           firstNonEmpty(outbound.Tag, outbound.Server),
			Protocol:       protocol,
			Server:         outbound.Server,
			Port:           int(outbound.ServerPort),
			Method:         outbound.Method,
			Password:       outbound.Password,
			UUID:           outbound.UUID,
			AlterID:        int(outbound.AlterID),
			Security:       outbound.Security,
			Flow:           outbound.Flow,
			UDP:            outbound.UDP,
			PacketEncoding: NormalizePacketEncoding(outbound.PacketEncoding),
			TLS:            outbound.TLS,
			Transport:      outbound.Transport,
		}
		if IsInformationalNodeName(node.Name) {
			continue
		}
		if err := validateNode(node); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
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
		if IsInformationalNodeName(node.Name) {
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
		Name     string      `json:"ps"`
		Server   string      `json:"add"`
		Port     flexibleInt `json:"port"`
		UUID     string      `json:"id"`
		AlterID  flexibleInt `json:"aid"`
		Network  string      `json:"net"`
		Security string      `json:"scy"`
		Host     string      `json:"host"`
		Path     string      `json:"path"`
		TLS      string      `json:"tls"`
		SNI      string      `json:"sni"`
		Type     string      `json:"type"`
	}
	if err := json.Unmarshal([]byte(decoded), &doc); err != nil {
		return ProxyNode{}, err
	}
	node := ProxyNode{
		Name:      firstNonEmpty(doc.Name, doc.Server),
		Protocol:  ProtocolVMess,
		Server:    doc.Server,
		Port:      int(doc.Port),
		UUID:      doc.UUID,
		AlterID:   int(doc.AlterID),
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
		Name:           firstNonEmpty(fragmentName(raw), u.Hostname()),
		Protocol:       protocol,
		Server:         u.Hostname(),
		Port:           port,
		Flow:           q.Get("flow"),
		Security:       q.Get("security"),
		PacketEncoding: NormalizePacketEncoding(firstNonEmpty(q.Get("packetEncoding"), q.Get("packet_encoding"))),
		Transport:      TransportOptions{Type: q.Get("type"), Path: firstNonEmpty(q.Get("path"), q.Get("serviceName")), Host: firstNonEmpty(q.Get("host"), q.Get("Host"))},
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
	if shouldEnableTLS(q) || alwaysTLSProtocol(protocol) {
		node.TLS = &TLSOptions{
			Enabled:     true,
			ServerName:  firstNonEmpty(q.Get("sni"), q.Get("peer"), q.Get("host")),
			Insecure:    tlsInsecureFromQuery(q),
			ALPN:        splitALPN(q.Get("alpn")),
			PublicKey:   q.Get("pbk"),
			ShortID:     q.Get("sid"),
			Fingerprint: firstNonEmpty(q.Get("fp"), q.Get("fingerprint")),
		}
	}
	return node, validateNode(node)
}

// alwaysTLSProtocol 的链接常常不带 security/sni 参数，但这些协议本身就跑在 TLS 上，
// 不把 TLS 打开 sing-box 会直接报 "TLS required" 启动失败。
func alwaysTLSProtocol(protocol string) bool {
	switch protocol {
	case ProtocolHysteria, ProtocolHysteria2, ProtocolTUIC, ProtocolAnyTLS:
		return true
	default:
		return false
	}
}

func splitALPN(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func shouldEnableTLS(q url.Values) bool {
	switch strings.ToLower(strings.TrimSpace(q.Get("security"))) {
	case "tls", "reality", "xtls":
		return true
	}
	return q.Get("sni") != "" ||
		q.Get("peer") != "" ||
		q.Get("pbk") != ""
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

// splitURIItems 只按换行/制表符切分：节点名（#后面的 fragment）里常带空格，
// 按空格切会把一条链接拆成两段，后半段解析失败后节点名也被截断。
func splitURIItems(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t'
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

func IsInformationalNodeName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	keywords := []string{
		"剩余流量",
		"流量",
		"套餐到期",
		"到期",
		"过期",
		"官网",
		"重置",
		"订阅",
		"traffic",
		"remaining",
		"expire",
		"expired",
		"reset",
		"official",
		"website",
		"subscription",
	}
	for _, keyword := range keywords {
		if strings.Contains(name, keyword) {
			return true
		}
	}
	return false
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
