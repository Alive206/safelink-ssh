package mobilecore

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCoreParsesSafeLinkSubscription(t *testing.T) {
	core := NewCore(t.TempDir())
	payload := `{"body":"{\"version\":1,\"tunnels\":[{\"name\":\"home\",\"mode\":\"vpn\",\"forward\":\"vpn.example.com:1562\",\"ssh\":{\"user\":\"admin\",\"password\":\"secret\"},\"tun\":{\"subnet\":\"10.8.0.2/24\",\"dns\":[\"1.1.1.1\"],\"auto_route\":true}}]}"}`

	var resp map[string]any
	if err := json.Unmarshal([]byte(core.Call("parseSubscription", payload)), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["ok"] != true {
		t.Fatalf("response = %#v", resp)
	}
	data := resp["data"].(map[string]any)
	if data["kind"] != "vpn" {
		t.Fatalf("kind = %v, want vpn", data["kind"])
	}
}

func TestCoreBuildsProxyConfig(t *testing.T) {
	core := NewCore(t.TempDir())
	payload := `{"mode":"global","node":{"name":"ss","protocol":"ss","server":"example.com","port":8388,"method":"aes-128-gcm","password":"secret"}}`
	got := core.Call("buildProxyConfig", payload)
	var resp struct {
		OK   bool `json:"ok"`
		Data struct {
			Config string `json:"config"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || !strings.Contains(resp.Data.Config, `"shadowsocks"`) {
		t.Fatalf("Call(buildProxyConfig) = %s", got)
	}
}
