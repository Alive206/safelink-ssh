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

func TestCoreProxyTestMethodsReturnStructuredFailures(t *testing.T) {
	core := NewCore(t.TempDir())
	payload := `{"node":{"name":"bad","protocol":"ss","server":"","port":0}}`
	for _, method := range []string{"testProxyNode", "speedTestProxyNode"} {
		got := core.Call(method, payload)
		var resp struct {
			OK   bool `json:"ok"`
			Data struct {
				Result struct {
					OK    bool   `json:"ok"`
					Error string `json:"error"`
				} `json:"result"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(got), &resp); err != nil {
			t.Fatalf("%s response is not JSON: %v", method, err)
		}
		if !resp.OK || resp.Data.Result.OK || resp.Data.Result.Error == "" {
			t.Fatalf("%s = %s", method, got)
		}
	}
}

func TestCoreBatchProxyTestMethodDecodesNodes(t *testing.T) {
	core := NewCore(t.TempDir())
	payload := `{"nodes":[{"name":"bad","protocol":"ss","server":"","port":0}]}`
	got := core.Call("testProxyNodes", payload)
	var resp struct {
		OK   bool `json:"ok"`
		Data struct {
			Results []struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || len(resp.Data.Results) != 1 || resp.Data.Results[0].OK || resp.Data.Results[0].Error == "" {
		t.Fatalf("Call(testProxyNodes) = %s", got)
	}
}
