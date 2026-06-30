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
