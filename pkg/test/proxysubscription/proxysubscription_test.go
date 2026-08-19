package proxysubscription_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/example/safelink/pkg/proxysubscription"
)

func TestParseClashYAMLImportsMainstreamProxyTypes(t *testing.T) {
	data := []byte(`
proxies:
  - name: ss-hk
    type: ss
    server: ss.example.com
    port: 8388
    cipher: aes-128-gcm
    password: secret
  - name: vless-sg
    type: vless
    server: vless.example.com
    port: 443
    uuid: 11111111-1111-1111-1111-111111111111
    tls: true
    servername: edge.example.com
    network: ws
    ws-opts:
      path: /ws
      headers:
        Host: edge.example.com
  - name: trojan-us
    type: trojan
    server: trojan.example.com
    port: 443
    password: pass
    sni: trojan.example.com
`)

	nodes, detected, err := proxysubscription.Parse(data, proxysubscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if detected != proxysubscription.FormatClashYAML {
		t.Fatalf("detected = %q, want %q", detected, proxysubscription.FormatClashYAML)
	}
	if len(nodes) != 3 {
		t.Fatalf("len(nodes) = %d, want 3", len(nodes))
	}
	if nodes[1].TLS == nil || !nodes[1].TLS.Enabled || nodes[1].Transport.Type != "ws" {
		t.Fatalf("vless tls/ws settings were not parsed: %+v", nodes[1])
	}
}

func TestParseBase64URIList(t *testing.T) {
	encodedSS := base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:secret@ss.example.com:8388"))
	vmessJSON := `{"v":"2","ps":"vmess-hk","add":"vmess.example.com","port":"443","id":"22222222-2222-2222-2222-222222222222","aid":"0","net":"ws","type":"none","host":"vmess.example.com","path":"/ray","tls":"tls","sni":"vmess.example.com"}`
	encodedVMess := base64.StdEncoding.EncodeToString([]byte(vmessJSON))
	body := "ss://" + encodedSS + "#ss-hk\nvmess://" + encodedVMess
	data := []byte(base64.StdEncoding.EncodeToString([]byte(body)))

	nodes, detected, err := proxysubscription.Parse(data, proxysubscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if detected != proxysubscription.FormatURIList {
		t.Fatalf("detected = %q, want %q", detected, proxysubscription.FormatURIList)
	}
	if len(nodes) != 2 {
		t.Fatalf("len(nodes) = %d, want 2", len(nodes))
	}
	if nodes[0].Name != "ss-hk" || nodes[0].Protocol != proxysubscription.ProtocolShadowsocks {
		t.Fatalf("unexpected shadowsocks node: %+v", nodes[0])
	}
	if nodes[1].Transport.Type != "ws" || nodes[1].TLS == nil || !nodes[1].TLS.Enabled {
		t.Fatalf("vmess transport/tls was not parsed: %+v", nodes[1])
	}
}

func TestParseSingleTrojanURI(t *testing.T) {
	nodes, detected, err := proxysubscription.Parse([]byte("trojan://secret@trojan.example.com:443?sni=edge.example.com#trojan-edge"), proxysubscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if detected != proxysubscription.FormatURIList {
		t.Fatalf("detected = %q, want %q", detected, proxysubscription.FormatURIList)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].Name != "trojan-edge" || nodes[0].Password != "secret" || nodes[0].TLS.ServerName != "edge.example.com" {
		t.Fatalf("unexpected trojan node: %+v", nodes[0])
	}
}

func TestParseMixedAnyTLSTrojanBase64Subscription(t *testing.T) {
	body := strings.Join([]string{
		"anytls://69ea31ea-6a04-4680-b56d-a005c6bef593@xsus.xs-us.net:8001/?type=tcp&insecure=0&fp=chrome&sni=sni.example.com#剩余流量：142.45 GB",
		"anytls://69ea31ea-6a04-4680-b56d-a005c6bef593@xsus.xs-us.net:8001/?type=tcp&insecure=0&fp=chrome&sni=sni.example.com#套餐到期：长期有效",
		"anytls://69ea31ea-6a04-4680-b56d-a005c6bef593@xsus.xs-us.net:8001/?type=tcp&insecure=0&fp=chrome&sni=sni.example.com#anytls-node",
		"trojan://69ea31ea-6a04-4680-b56d-a005c6bef593@zl.example.com:46613?allowInsecure=1&peer=us01.example.com&sni=us01.example.com&type=tcp#trojan-node",
		"unsupported://bad-node",
	}, "\n")
	data := []byte(base64.StdEncoding.EncodeToString([]byte(body)))

	nodes, detected, err := proxysubscription.Parse(data, proxysubscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if detected != proxysubscription.FormatURIList {
		t.Fatalf("detected = %q, want %q", detected, proxysubscription.FormatURIList)
	}
	if len(nodes) != 2 {
		t.Fatalf("len(nodes) = %d, want 2", len(nodes))
	}
	if nodes[0].Protocol != proxysubscription.ProtocolAnyTLS || nodes[0].Password == "" || nodes[0].TLS == nil {
		t.Fatalf("unexpected anytls node: %+v", nodes[0])
	}
	if nodes[0].TLS.Fingerprint != "chrome" {
		t.Fatalf("anytls fingerprint was not parsed: %+v", nodes[0].TLS)
	}
	if nodes[1].Protocol != proxysubscription.ProtocolTrojan || !nodes[1].TLS.Insecure {
		t.Fatalf("unexpected trojan node: %+v", nodes[1])
	}
}

// TestParseClashYAMLKeepsGoodNodesWhenOneNodeIsMalformed 盯的是容错：机场订阅里常有
// port 写成字符串、字段缺失的节点，以前任意一个写法特殊就会把整份订阅拖死。
func TestParseClashYAMLKeepsGoodNodesWhenOneNodeIsMalformed(t *testing.T) {
	data := []byte(`
proxies:
  - name: port-as-string
    type: vless
    server: a.example.com
    port: "443"
    uuid: 11111111-1111-1111-1111-111111111111
    network: ws
    tls: true
    skip-cert-verify: true
    client-fingerprint: chrome
    ws-opts:
      path: /ws
  - name: broken-vmess
    type: vmess
    server: b.example.com
    port: 443
  - name: grpc-node
    type: vless
    server: c.example.com
    port: 443
    uuid: 11111111-1111-1111-1111-111111111111
    network: grpc
    tls: true
    grpc-opts:
      grpc-service-name: grpcsvc
`)

	nodes, detected, err := proxysubscription.Parse(data, proxysubscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if detected != proxysubscription.FormatClashYAML {
		t.Fatalf("detected = %q, want %q", detected, proxysubscription.FormatClashYAML)
	}
	if len(nodes) != 2 {
		t.Fatalf("len(nodes) = %d, want 2 (the malformed node should be skipped)", len(nodes))
	}
	if nodes[0].Port != 443 {
		t.Fatalf("quoted port was not parsed: %+v", nodes[0])
	}
	if !nodes[0].TLS.Insecure || nodes[0].TLS.Fingerprint != "chrome" {
		t.Fatalf("skip-cert-verify/client-fingerprint were not parsed: %+v", nodes[0].TLS)
	}
	if nodes[1].Transport.Type != "grpc" || nodes[1].Transport.Path != "grpcsvc" {
		t.Fatalf("grpc-opts were not parsed: %+v", nodes[1].Transport)
	}
}

func TestParseClashYAMLFailsWhenEveryNodeIsMalformed(t *testing.T) {
	data := []byte(`
proxies:
  - name: broken-vmess
    type: vmess
    server: b.example.com
    port: 443
`)

	if _, _, err := proxysubscription.Parse(data, proxysubscription.FormatAuto); err == nil {
		t.Fatal("Parse should fail when no node survives validation")
	}
}

// TestParseVLESSRealityURIKeepsRealityParameters 盯的是机场最常见的 vless+reality 链接：
// 链接里只有 pbk/sid，既不带 security 也不带 fp。
func TestParseVLESSRealityURIKeepsRealityParameters(t *testing.T) {
	uri := "vless://11111111-1111-1111-1111-111111111111@vless.example.com:443?encryption=none&type=grpc&serviceName=grpcsvc&pbk=jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0&sid=1234#vless-reality"

	nodes, _, err := proxysubscription.Parse([]byte(uri), proxysubscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	node := nodes[0]
	if node.TLS == nil || !node.TLS.Enabled {
		t.Fatalf("reality link must enable TLS: %+v", node.TLS)
	}
	if node.TLS.PublicKey == "" || node.TLS.ShortID != "1234" {
		t.Fatalf("reality parameters were not parsed: %+v", node.TLS)
	}
	if node.Transport.Type != "grpc" || node.Transport.Path != "grpcsvc" {
		t.Fatalf("grpc service name was not parsed: %+v", node.Transport)
	}
}

// TestParseURIListEnablesTLSForProtocolsThatRequireIt 盘 hysteria2/tuic/anytls 的裸链接：
// 这些协议本身就跑在 TLS 上，不把 TLS 打开 sing-box 会报 TLS required。
func TestParseURIListEnablesTLSForProtocolsThatRequireIt(t *testing.T) {
	body := strings.Join([]string{
		"hysteria2://pass@hy2.example.com:443?insecure=1#hy2-no-sni",
		"tuic://11111111-1111-1111-1111-111111111111:pass@tuic.example.com:443#tuic-no-sni",
		"anytls://pass@anytls.example.com:443#anytls-no-sni",
	}, "\n")

	nodes, _, err := proxysubscription.Parse([]byte(body), proxysubscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("len(nodes) = %d, want 3", len(nodes))
	}
	for _, node := range nodes {
		if node.TLS == nil || !node.TLS.Enabled {
			t.Fatalf("%s node must enable TLS: %+v", node.Protocol, node.TLS)
		}
	}
}

// TestParseVMessURIAcceptsNumericPortAndAlterID 盯的是面板差异：v2rayN 导出的 vmess
// 链接把 port/aid 写成字符串，而不少机场面板直接输出 JSON 数字。
func TestParseVMessURIAcceptsNumericPortAndAlterID(t *testing.T) {
	vmessJSON := `{"v":"2","ps":"vmess-numeric","add":"vmess.example.com","port":443,"id":"22222222-2222-2222-2222-222222222222","aid":0,"net":"ws","type":"none","host":"vmess.example.com","path":"/ray","tls":"tls"}`
	uri := "vmess://" + base64.StdEncoding.EncodeToString([]byte(vmessJSON))

	nodes, _, err := proxysubscription.Parse([]byte(uri), proxysubscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].Port != 443 {
		t.Fatalf("numeric port was not parsed: %+v", nodes[0])
	}
}

func TestNormalizePacketEncodingRejectsUnknownValues(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "xudp", want: proxysubscription.PacketEncodingXUDP},
		{in: " XUDP ", want: proxysubscription.PacketEncodingXUDP},
		{in: "packetaddr", want: proxysubscription.PacketEncodingPacketAddr},
		{in: "packet-addr", want: proxysubscription.PacketEncodingPacketAddr},
		{in: "none", want: proxysubscription.PacketEncodingDisabled},
		{in: "", want: ""},
		// 内核遇到认不出的值会直接 FATAL，所以必须当成“没指定”丢掉
		{in: "bogus", want: ""},
	}
	for _, tt := range tests {
		if got := proxysubscription.NormalizePacketEncoding(tt.in); got != tt.want {
			t.Fatalf("NormalizePacketEncoding(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParsePacketEncodingFromClashAndURI(t *testing.T) {
	data := []byte(`
proxies:
  - name: vmess-packetaddr
    type: vmess
    server: a.example.com
    port: 443
    uuid: 11111111-1111-1111-1111-111111111111
    packet-encoding: packetaddr
  - name: vless-xudp-flag
    type: vless
    server: b.example.com
    port: 443
    uuid: 11111111-1111-1111-1111-111111111111
    xudp: true
  - name: vless-bogus-encoding
    type: vless
    server: c.example.com
    port: 443
    uuid: 11111111-1111-1111-1111-111111111111
    packet-encoding: bogus
`)

	nodes, _, err := proxysubscription.Parse(data, proxysubscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("len(nodes) = %d, want 3", len(nodes))
	}
	if nodes[0].PacketEncoding != proxysubscription.PacketEncodingPacketAddr {
		t.Fatalf("packet-encoding was not parsed: %+v", nodes[0])
	}
	if nodes[1].PacketEncoding != proxysubscription.PacketEncodingXUDP {
		t.Fatalf("xudp flag was not parsed: %+v", nodes[1])
	}
	if nodes[2].PacketEncoding != "" {
		t.Fatalf("unknown packet-encoding must be dropped: %+v", nodes[2])
	}

	uriNodes, _, err := proxysubscription.Parse([]byte("vless://11111111-1111-1111-1111-111111111111@d.example.com:443?packetEncoding=xudp#vless-uri"), proxysubscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if uriNodes[0].PacketEncoding != proxysubscription.PacketEncodingXUDP {
		t.Fatalf("packetEncoding query was not parsed: %+v", uriNodes[0])
	}
}

func TestParseSingBoxJSONOutbounds(t *testing.T) {
	data := []byte(`{
  "outbounds": [
    {
      "type": "hysteria2",
      "tag": "hy2-jp",
      "server": "hy2.example.com",
      "server_port": 443,
      "password": "secret",
      "tls": {"enabled": true, "server_name": "hy2.example.com"}
    },
    {
      "type": "direct",
      "tag": "direct"
    }
  ]
}`)

	nodes, detected, err := proxysubscription.Parse(data, proxysubscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if detected != proxysubscription.FormatSingBoxJSON {
		t.Fatalf("detected = %q, want %q", detected, proxysubscription.FormatSingBoxJSON)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].Protocol != proxysubscription.ProtocolHysteria2 || nodes[0].Port != 443 {
		t.Fatalf("unexpected hysteria2 node: %+v", nodes[0])
	}
}
