package proxycore_test

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/safelink/client/internal/proxycore"
	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/proxysubscription"
)

func TestBuildConfigCreatesLocalInboundsAndSelectedOutbound(t *testing.T) {
	node := proxysubscription.ProxyNode{
		Name:     "vless-sg",
		Protocol: proxysubscription.ProtocolVLESS,
		Server:   "vless.example.com",
		Port:     443,
		UUID:     "11111111-1111-1111-1111-111111111111",
		TLS: &proxysubscription.TLSOptions{
			Enabled:    true,
			ServerName: "edge.example.com",
		},
		Transport: proxysubscription.TransportOptions{
			Type: "ws",
			Path: "/ws",
			Host: "edge.example.com",
		},
	}

	data, err := proxycore.BuildConfig(node, proxycore.Options{
		SOCKSAddr: "127.0.0.1:10808",
		HTTPAddr:  "127.0.0.1:10809",
	})
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not JSON: %v\n%s", err, string(data))
	}
	inbounds := cfg["inbounds"].([]any)
	if len(inbounds) != 2 {
		t.Fatalf("len(inbounds) = %d, want 2", len(inbounds))
	}
	outbounds := cfg["outbounds"].([]any)
	selected := outbounds[0].(map[string]any)
	if selected["type"] != "vless" || selected["server"] != "vless.example.com" || selected["server_port"].(float64) != 443 {
		t.Fatalf("unexpected outbound: %+v", selected)
	}
	if selected["tls"].(map[string]any)["server_name"] != "edge.example.com" {
		t.Fatalf("TLS server name missing: %+v", selected["tls"])
	}
	if selected["transport"].(map[string]any)["path"] != "/ws" {
		t.Fatalf("WS transport missing: %+v", selected["transport"])
	}
}

func TestBuildConfigRejectsUnsupportedNodes(t *testing.T) {
	_, err := proxycore.BuildConfig(proxysubscription.ProxyNode{Name: "bad", Protocol: "unknown"}, proxycore.Options{})
	if err == nil {
		t.Fatalf("BuildConfig should reject unsupported protocols")
	}
}

func TestBuildConfigAppliesProxyModeRoute(t *testing.T) {
	node := proxysubscription.ProxyNode{
		Name:     "ss-hk",
		Protocol: proxysubscription.ProtocolShadowsocks,
		Server:   "ss.example.com",
		Port:     8388,
		Method:   "2022-blake3-aes-128-gcm",
		Password: "secret",
	}
	tests := []struct {
		name string
		mode string
		want string
	}{
		{name: "global", mode: proxycore.ProxyModeGlobal, want: "selected"},
		{name: "direct", mode: proxycore.ProxyModeDirect, want: "direct"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := proxycore.BuildConfig(node, proxycore.Options{Mode: tt.mode})
			if err != nil {
				t.Fatalf("BuildConfig returned error: %v", err)
			}
			var cfg map[string]any
			if err := json.Unmarshal(data, &cfg); err != nil {
				t.Fatalf("config is not JSON: %v\n%s", err, string(data))
			}
			route := cfg["route"].(map[string]any)
			if route["final"] != tt.want {
				t.Fatalf("route.final = %q, want %q", route["final"], tt.want)
			}
		})
	}
}

func TestBuildConfigAppliesEditableRuleModeRules(t *testing.T) {
	node := proxysubscription.ProxyNode{
		Name:     "ss-hk",
		Protocol: proxysubscription.ProtocolShadowsocks,
		Server:   "ss.example.com",
		Port:     8388,
		Method:   "aes-128-gcm",
		Password: "secret",
	}
	data, err := proxycore.BuildConfig(node, proxycore.Options{
		Mode: proxycore.ProxyModeRule,
		RuleModeRules: []config.ProxyRule{
			{Name: "direct suffix", Type: config.ProxyRuleTypeDomainSuffix, Value: "example.cn", Outbound: config.ProxyRuleOutboundDirect, Enabled: true},
			{Name: "proxy keyword", Type: config.ProxyRuleTypeDomainKeyword, Value: "google", Outbound: config.ProxyRuleOutboundProxy, Enabled: true},
			{Name: "disabled", Type: config.ProxyRuleTypeDomain, Value: "disabled.example", Outbound: config.ProxyRuleOutboundBlock, Enabled: false},
		},
	})
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not JSON: %v\n%s", err, string(data))
	}
	route := cfg["route"].(map[string]any)
	rules := route["rules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("len(route.rules) = %d, want 2", len(rules))
	}
	directRule := rules[0].(map[string]any)
	if directRule["outbound"] != "direct" || directRule["domain_suffix"].([]any)[0] != "example.cn" {
		t.Fatalf("unexpected direct rule: %+v", directRule)
	}
	proxyRule := rules[1].(map[string]any)
	if proxyRule["outbound"] != "selected" || proxyRule["domain_keyword"].([]any)[0] != "google" {
		t.Fatalf("unexpected proxy rule: %+v", proxyRule)
	}
	if _, ok := route["rule_set"]; ok {
		t.Fatalf("custom rules without rule_set should not add remote rule sets: %+v", route["rule_set"])
	}
}

func TestBuildConfigIncludesDefaultRuleSetsForChinaBypass(t *testing.T) {
	node := proxysubscription.ProxyNode{
		Name:     "ss-hk",
		Protocol: proxysubscription.ProtocolShadowsocks,
		Server:   "ss.example.com",
		Port:     8388,
		Method:   "aes-128-gcm",
		Password: "secret",
	}
	data, err := proxycore.BuildConfig(node, proxycore.Options{
		Mode:    proxycore.ProxyModeRule,
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not JSON: %v\n%s", err, string(data))
	}

	route := cfg["route"].(map[string]any)
	if route["final"] != "selected" {
		t.Fatalf("route.final = %q, want selected", route["final"])
	}
	rules := route["rules"].([]any)
	if !routeRulesContainRuleSet(rules, config.ProxyRuleSetGeositeGeolocationNotCN, "selected") {
		t.Fatalf("route.rules missing foreign geosite proxy rule: %+v", rules)
	}
	if !routeRulesContainRuleSet(rules, config.ProxyRuleSetGeositeGeolocationCN, "direct") {
		t.Fatalf("route.rules missing China geosite direct rule: %+v", rules)
	}
	if !routeRulesContainRuleSet(rules, config.ProxyRuleSetGeoIPCN, "direct") {
		t.Fatalf("route.rules missing China geoip direct rule: %+v", rules)
	}

	ruleSets := route["rule_set"].([]any)
	if !localRuleSetsContain(ruleSets, config.ProxyRuleSetGeositeGeolocationNotCN) ||
		!localRuleSetsContain(ruleSets, config.ProxyRuleSetGeositeGeolocationCN) ||
		!localRuleSetsContain(ruleSets, config.ProxyRuleSetGeoIPCN) {
		t.Fatalf("route.rule_set missing expected local definitions: %+v", ruleSets)
	}
	experimental := cfg["experimental"].(map[string]any)
	cacheFile := experimental["cache_file"].(map[string]any)
	if cacheFile["enabled"] != true {
		t.Fatalf("cache_file.enabled = %v, want true", cacheFile["enabled"])
	}
}

// 规则集必须是内置的本地文件。以前是 remote 类型现下载，分流规则本身成了能上网的
// 前提：拉不到就 FATAL 起不来。现在启动不碰网络，文件必须真实落盘且内容非空。
func TestBuildConfigUsesEmbeddedLocalRuleSets(t *testing.T) {
	node := proxysubscription.ProxyNode{
		Name:     "ss-hk",
		Protocol: proxysubscription.ProtocolShadowsocks,
		Server:   "ss.example.com",
		Port:     8388,
		Method:   "aes-128-gcm",
		Password: "secret",
	}
	ruleSetDir := filepath.Join(t.TempDir(), "rule-sets")
	data, err := proxycore.BuildConfig(node, proxycore.Options{
		Mode:       proxycore.ProxyModeRule,
		WorkDir:    t.TempDir(),
		RuleSetDir: ruleSetDir,
	})
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not JSON: %v\n%s", err, string(data))
	}

	ruleSets := cfg["route"].(map[string]any)["rule_set"].([]any)
	if len(ruleSets) == 0 {
		t.Fatal("route.rule_set is empty, expected default local definitions")
	}
	for _, raw := range ruleSets {
		ruleSet := raw.(map[string]any)
		if ruleSet["type"] != "local" {
			t.Fatalf("rule_set %v type = %v, want local", ruleSet["tag"], ruleSet["type"])
		}
		// remote 专属字段一个都不该留下，留着 sing-box 又会去联网
		if _, ok := ruleSet["url"]; ok {
			t.Fatalf("rule_set %v still carries a url: %+v", ruleSet["tag"], ruleSet)
		}
		if _, ok := ruleSet["download_detour"]; ok {
			t.Fatalf("rule_set %v still carries download_detour: %+v", ruleSet["tag"], ruleSet)
		}
		path, _ := ruleSet["path"].(string)
		if filepath.Dir(path) != ruleSetDir {
			t.Fatalf("rule_set %v path = %q, want a file under %q", ruleSet["tag"], path, ruleSetDir)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("rule_set %v file was not written: %v", ruleSet["tag"], err)
		}
		if info.Size() == 0 {
			t.Fatalf("rule_set %v file is empty", ruleSet["tag"])
		}
	}
}

// cache_file 要无条件开并带显式 path：规则集的 .srs 缓存全靠它，冷启动一次要多付好几秒。
func TestBuildConfigAlwaysEnablesCacheFileWithPath(t *testing.T) {
	node := proxysubscription.ProxyNode{
		Name:     "ss-hk",
		Protocol: proxysubscription.ProtocolShadowsocks,
		Server:   "ss.example.com",
		Port:     8388,
		Method:   "aes-128-gcm",
		Password: "secret",
	}
	workDir := t.TempDir()
	for _, mode := range []string{proxycore.ProxyModeRule, proxycore.ProxyModeGlobal, proxycore.ProxyModeDirect} {
		t.Run(mode, func(t *testing.T) {
			data, err := proxycore.BuildConfig(node, proxycore.Options{Mode: mode, WorkDir: workDir})
			if err != nil {
				t.Fatalf("BuildConfig returned error: %v", err)
			}
			var cfg map[string]any
			if err := json.Unmarshal(data, &cfg); err != nil {
				t.Fatalf("config is not JSON: %v\n%s", err, string(data))
			}
			cacheFile := cfg["experimental"].(map[string]any)["cache_file"].(map[string]any)
			if cacheFile["enabled"] != true {
				t.Fatalf("cache_file.enabled = %v, want true", cacheFile["enabled"])
			}
			want := filepath.Join(workDir, "cache.db")
			if cacheFile["path"] != want {
				t.Fatalf("cache_file.path = %v, want %q", cacheFile["path"], want)
			}
		})
	}
}

func TestBuildConfigOmitsAnyTLSTransportAndKeepsUTLSFingerprint(t *testing.T) {
	node := proxysubscription.ProxyNode{
		Name:      "anytls-hk",
		Protocol:  proxysubscription.ProtocolAnyTLS,
		Server:    "anytls.example.com",
		Port:      443,
		Password:  "secret",
		Transport: proxysubscription.TransportOptions{Type: "tcp"},
		TLS: &proxysubscription.TLSOptions{
			Enabled:     true,
			ServerName:  "sni.example.com",
			Fingerprint: "chrome",
		},
	}

	data, err := proxycore.BuildConfig(node, proxycore.Options{})
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not JSON: %v\n%s", err, string(data))
	}
	selected := cfg["outbounds"].([]any)[0].(map[string]any)
	if _, ok := selected["transport"]; ok {
		t.Fatalf("AnyTLS tcp transport should be omitted: %+v", selected["transport"])
	}
	tls := selected["tls"].(map[string]any)
	utls := tls["utls"].(map[string]any)
	if utls["fingerprint"] != "chrome" {
		t.Fatalf("utls fingerprint missing: %+v", tls)
	}
}

func TestBuildConfigAddsDefaultUTLSFingerprintForReality(t *testing.T) {
	node := proxysubscription.ProxyNode{
		Name:     "vless-reality",
		Protocol: proxysubscription.ProtocolVLESS,
		Server:   "reality.example.com",
		Port:     443,
		UUID:     "11111111-1111-1111-1111-111111111111",
		TLS: &proxysubscription.TLSOptions{
			Enabled:    true,
			ServerName: "www.microsoft.com",
			PublicKey:  "jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0",
			ShortID:    "1234",
		},
	}

	data, err := proxycore.BuildConfig(node, proxycore.Options{})
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not JSON: %v\n%s", err, string(data))
	}
	tls := cfg["outbounds"].([]any)[0].(map[string]any)["tls"].(map[string]any)
	if tls["reality"].(map[string]any)["enabled"] != true {
		t.Fatalf("reality was not enabled: %+v", tls)
	}
	utls, ok := tls["utls"].(map[string]any)
	if !ok || utls["fingerprint"] == "" {
		t.Fatalf("reality client requires a uTLS fingerprint: %+v", tls)
	}
}

func TestBuildConfigEnablesTLSForProtocolsThatRequireIt(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
	}{
		{name: "hysteria2", protocol: proxysubscription.ProtocolHysteria2},
		{name: "tuic", protocol: proxysubscription.ProtocolTUIC},
		{name: "anytls", protocol: proxysubscription.ProtocolAnyTLS},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := proxysubscription.ProxyNode{
				Name:     tt.name,
				Protocol: tt.protocol,
				Server:   "node.example.com",
				Port:     443,
				UUID:     "11111111-1111-1111-1111-111111111111",
				Password: "secret",
			}
			data, err := proxycore.BuildConfig(node, proxycore.Options{})
			if err != nil {
				t.Fatalf("BuildConfig returned error: %v", err)
			}
			var cfg map[string]any
			if err := json.Unmarshal(data, &cfg); err != nil {
				t.Fatalf("config is not JSON: %v\n%s", err, string(data))
			}
			selected := cfg["outbounds"].([]any)[0].(map[string]any)
			tls, ok := selected["tls"].(map[string]any)
			if !ok || tls["enabled"] != true {
				t.Fatalf("%s outbound without TLS fails to start: %+v", tt.protocol, selected)
			}
		})
	}
}

// TestBuildConfigForAirportURIsPassesCoreCheck 把机场里常见的链接写法喂给解析器，
// 再交给真实 sing-box check：内核对 REALITY、TLS 的硬性要求只有它能验出来。
func TestBuildConfigForAirportURIsPassesCoreCheck(t *testing.T) {
	vmessJSON := `{"v":"2","ps":"vmess-ws-tls","add":"vmess.example.com","port":"443","id":"11111111-1111-1111-1111-111111111111","aid":"0","net":"ws","type":"none","host":"vmess.example.com","path":"/ray","tls":"tls","sni":"vmess.example.com"}`
	uris := strings.Join([]string{
		"vless://11111111-1111-1111-1111-111111111111@example.com:443?encryption=none&security=reality&sni=www.microsoft.com&flow=xtls-rprx-vision&pbk=jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0&sid=1234&type=tcp#reality-no-fp",
		"vless://11111111-1111-1111-1111-111111111111@example.com:443?encryption=none&security=tls&sni=a.example.com&type=ws&host=a.example.com&path=%2Fws#香港 01 IEPL",
		"vless://11111111-1111-1111-1111-111111111111@example.com:443?encryption=none&security=reality&pbk=jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0&type=grpc&serviceName=grpcsvc#reality-grpc",
		"vmess://" + base64.StdEncoding.EncodeToString([]byte(vmessJSON)),
		"hysteria2://pass@example.com:443?insecure=1#hy2-no-sni",
		"tuic://11111111-1111-1111-1111-111111111111:pass@example.com:443#tuic-no-sni",
		"anytls://pass@example.com:443#anytls-no-sni",
	}, "\n")

	nodes, _, err := proxysubscription.Parse([]byte(uris), proxysubscription.FormatURIList)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(nodes) != 7 {
		t.Fatalf("len(nodes) = %d, want 7", len(nodes))
	}
	if nodes[1].Name != "香港 01 IEPL" {
		t.Fatalf("node name with spaces was truncated: %q", nodes[1].Name)
	}

	corePath := locateRealCore(t)
	for _, node := range nodes {
		data, err := proxycore.BuildConfig(node, proxycore.Options{CorePath: corePath, WorkDir: t.TempDir()})
		if err != nil {
			t.Errorf("%s: BuildConfig returned error: %v", node.Name, err)
			continue
		}
		configPath := filepath.Join(t.TempDir(), "sing-box.json")
		if err := os.WriteFile(configPath, data, 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		if output, err := exec.Command(corePath, "check", "-c", configPath).CombinedOutput(); err != nil {
			t.Errorf("%s: sing-box check failed: %v: %s", node.Name, err, strings.TrimSpace(string(output)))
		}
	}
}

// TestBuildConfigSetsPacketEncodingForVMessAndVLESS 盯的是 UDP 转发：机场链接基本不带
// packetEncoding，而服务端多为 Xray，不默认上 xudp 游戏/语音这类流量会出问题。
func TestBuildConfigSetsPacketEncodingForVMessAndVLESS(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		node     string
		want     any
	}{
		{name: "vless default", protocol: proxysubscription.ProtocolVLESS, want: "xudp"},
		{name: "vmess default", protocol: proxysubscription.ProtocolVMess, want: "xudp"},
		{name: "node overrides", protocol: proxysubscription.ProtocolVLESS, node: "packetaddr", want: "packetaddr"},
		{name: "node disables", protocol: proxysubscription.ProtocolVLESS, node: proxysubscription.PacketEncodingDisabled, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := proxycore.BuildConfig(proxysubscription.ProxyNode{
				Name:           tt.name,
				Protocol:       tt.protocol,
				Server:         "node.example.com",
				Port:           443,
				UUID:           "11111111-1111-1111-1111-111111111111",
				PacketEncoding: tt.node,
			}, proxycore.Options{})
			if err != nil {
				t.Fatalf("BuildConfig returned error: %v", err)
			}
			var cfg map[string]any
			if err := json.Unmarshal(data, &cfg); err != nil {
				t.Fatalf("config is not JSON: %v\n%s", err, string(data))
			}
			selected := cfg["outbounds"].([]any)[0].(map[string]any)
			if got := selected["packet_encoding"]; got != tt.want {
				t.Fatalf("packet_encoding = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildConfigOmitsPacketEncodingForOtherProtocols(t *testing.T) {
	data, err := proxycore.BuildConfig(proxysubscription.ProxyNode{
		Name:     "trojan-us",
		Protocol: proxysubscription.ProtocolTrojan,
		Server:   "trojan.example.com",
		Port:     443,
		Password: "secret",
	}, proxycore.Options{})
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not JSON: %v\n%s", err, string(data))
	}
	selected := cfg["outbounds"].([]any)[0].(map[string]any)
	if _, ok := selected["packet_encoding"]; ok {
		t.Fatalf("packet_encoding only applies to vmess/vless: %+v", selected)
	}
}

func TestFindCorePathReturnsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "sing-box.exe")
	if err := os.WriteFile(bin, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}
	got := proxycore.FindCorePath(dir)
	if got == "" {
		t.Fatal("FindCorePath returned empty path")
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("FindCorePath returned relative path %q", got)
	}
	if !strings.EqualFold(got, bin) {
		t.Fatalf("FindCorePath = %q, want %q", got, bin)
	}
}

// TestNodeMeasuresDelayThroughCoreAPI 盯的是 QUIC 协议的探测路径：hysteria2 只监听 UDP，
// 没有 TCP 端口可握手，只能拉起 core 用 Clash API 量延迟。
func TestNodeMeasuresDelayThroughCoreAPI(t *testing.T) {
	helper := writeHelperCore(t)
	node := proxysubscription.ProxyNode{
		Name:     "hy2-jp",
		Protocol: proxysubscription.ProtocolHysteria2,
		Server:   "198.51.100.10",
		Port:     8388,
		Password: "secret",
	}

	result := proxycore.TestNode(t.Context(), node, proxycore.Options{
		CorePath: helper,
		WorkDir:  t.TempDir(),
	}, 3*time.Second)
	if !result.OK {
		t.Fatalf("TestNode failed: %+v", result)
	}
	if result.LatencyMS <= 0 {
		t.Fatalf("LatencyMS = %d, want > 0", result.LatencyMS)
	}
}

// TestNodeMeasuresTCPHandshakeWithoutCore 要求 TCP 协议的测速不拉 core：
// CorePath 指向一个不存在的文件，能测出延迟就证明走的是 tcping 分支。
func TestNodeMeasuresTCPHandshakeWithoutCore(t *testing.T) {
	host, port := listenLocalTCP(t)
	node := proxysubscription.ProxyNode{
		Name:     "vless-local",
		Protocol: proxysubscription.ProtocolVLESS,
		Server:   host,
		Port:     port,
		UUID:     "11111111-1111-1111-1111-111111111111",
	}

	result := proxycore.TestNode(t.Context(), node, proxycore.Options{
		CorePath: filepath.Join(t.TempDir(), "missing-core.exe"),
		WorkDir:  t.TempDir(),
	}, 2*time.Second)
	if !result.OK {
		t.Fatalf("TestNode failed: %+v", result)
	}
	if result.LatencyMS <= 0 {
		t.Fatalf("LatencyMS = %d, want > 0", result.LatencyMS)
	}
}

func TestNodeReportsErrorWhenTCPPortIsClosed(t *testing.T) {
	host, port := reserveClosedTCPPort(t)
	node := proxysubscription.ProxyNode{
		Name:     "vless-dead",
		Protocol: proxysubscription.ProtocolVLESS,
		Server:   host,
		Port:     port,
		UUID:     "11111111-1111-1111-1111-111111111111",
	}

	result := proxycore.TestNode(t.Context(), node, proxycore.Options{WorkDir: t.TempDir()}, 2*time.Second)
	if result.OK {
		t.Fatalf("TestNode should fail on a closed port: %+v", result)
	}
	if !strings.Contains(result.Error, "tcp connect") {
		t.Fatalf("Error = %q, want a tcp connect failure", result.Error)
	}
}

// TestNodesSplitsTCPAndCoreProtocols 验证批量测速的分流：TCP 协议自己握手，
// QUIC 协议仍然交给 core（这里 core 缺失，所以错误不会是 tcp connect）。
func TestNodesSplitsTCPAndCoreProtocols(t *testing.T) {
	host, port := listenLocalTCP(t)
	nodes := []proxysubscription.ProxyNode{
		{
			Name:     "vless-local",
			Protocol: proxysubscription.ProtocolVLESS,
			Server:   host,
			Port:     port,
			UUID:     "11111111-1111-1111-1111-111111111111",
		},
		{
			Name:     "no-port",
			Protocol: proxysubscription.ProtocolVLESS,
			Server:   host,
		},
		{
			Name:     "hy2-jp",
			Protocol: proxysubscription.ProtocolHysteria2,
			Server:   "198.51.100.10",
			Port:     8388,
			Password: "secret",
		},
	}

	var emitted int
	results := proxycore.TestNodes(t.Context(), nodes, proxycore.Options{
		CorePath: filepath.Join(t.TempDir(), "missing-core.exe"),
		WorkDir:  t.TempDir(),
	}, 2*time.Second, func(proxycore.TestResult) { emitted++ })
	if len(results) != len(nodes) {
		t.Fatalf("len(results) = %d, want %d", len(results), len(nodes))
	}
	if emitted != len(nodes) {
		t.Fatalf("onResult called %d times, want %d", emitted, len(nodes))
	}
	if !results[0].OK || results[0].LatencyMS <= 0 {
		t.Fatalf("tcp node was not measured: %+v", results[0])
	}
	if results[1].OK || !strings.Contains(results[1].Error, "invalid server or port") {
		t.Fatalf("invalid node should be rejected: %+v", results[1])
	}
	if results[2].OK || strings.Contains(results[2].Error, "tcp connect") {
		t.Fatalf("quic node should go through the core path: %+v", results[2])
	}
}

func listenLocalTCP(t *testing.T) (string, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse listener port: %v", err)
	}
	return host, port
}

// reserveClosedTCPPort 占一个端口再立刻释放，得到一个肯定没人监听的地址。
func reserveClosedTCPPort(t *testing.T) (string, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		t.Fatalf("split listener address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		_ = listener.Close()
		t.Fatalf("parse listener port: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return host, port
}

// locateRealCore 找真实的 sing-box，没有就跳过（CI 上不一定带内核二进制）。
func locateRealCore(t *testing.T) string {
	t.Helper()
	if corePath := proxycore.FindCorePath(""); corePath != "" {
		return corePath
	}
	corePath := filepath.Join("..", "..", "build", "bin", "sing-box.exe")
	if _, err := os.Stat(corePath); err != nil {
		t.Skip("sing-box executable not found, skip core check")
	}
	abs, err := filepath.Abs(corePath)
	if err != nil {
		t.Fatalf("resolve core path: %v", err)
	}
	return abs
}

func routeRulesContainRuleSet(rules []any, tag, outbound string) bool {
	for _, raw := range rules {
		rule := raw.(map[string]any)
		values, ok := rule["rule_set"].([]any)
		if !ok || len(values) == 0 {
			continue
		}
		if values[0] == tag && rule["outbound"] == outbound {
			return true
		}
	}
	return false
}

func localRuleSetsContain(ruleSets []any, tag string) bool {
	for _, raw := range ruleSets {
		ruleSet := raw.(map[string]any)
		if ruleSet["tag"] == tag && ruleSet["type"] == "local" && ruleSet["format"] == "binary" {
			return true
		}
	}
	return false
}

func writeHelperCore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	helper := filepath.Join(dir, "sing-box-helper.exe")
	content := `package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
)

func main() {
	configPath := ""
	for i, arg := range os.Args {
		if arg == "-c" && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
			break
		}
	}
	if configPath == "" {
		fmt.Fprintln(os.Stderr, "missing -c")
		os.Exit(2)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var cfg struct {
		Inbounds []struct {
			Type       string ` + "`json:\"type\"`" + `
			Listen     string ` + "`json:\"listen\"`" + `
			ListenPort int    ` + "`json:\"listen_port\"`" + `
		} ` + "`json:\"inbounds\"`" + `
		Experimental struct {
			ClashAPI struct {
				ExternalController string ` + "`json:\"external_controller\"`" + `
			} ` + "`json:\"clash_api\"`" + `
		} ` + "`json:\"experimental\"`" + `
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) > 1 && os.Args[1] == "check" {
		return
	}
	addr := ""
	for _, inbound := range cfg.Inbounds {
		if inbound.Type == "http" {
			addr = net.JoinHostPort(inbound.Listen, strconv.Itoa(inbound.ListenPort))
			break
		}
	}
	if addr == "" {
		fmt.Fprintln(os.Stderr, "http inbound not found")
		os.Exit(2)
	}
	inboundServer := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	go func() {
		if err := inboundServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}()
	controllerAddr := cfg.Experimental.ClashAPI.ExternalController
	if controllerAddr == "" {
		fmt.Fprintln(os.Stderr, "clash api controller not found")
		os.Exit(2)
	}
	controller := &http.Server{
		Addr: controllerAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/proxies/selected/delay" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(` + "`{\"delay\":42}`" + `))
		}),
	}
	if err := controller.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
`
	if err := os.WriteFile(source, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", helper, source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build helper core: %v\n%s", err, out)
	}
	return helper
}
