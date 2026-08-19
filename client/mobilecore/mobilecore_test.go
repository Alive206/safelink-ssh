package mobilecore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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

func TestMobileVPNSampleTrafficComputesSpeed(t *testing.T) {
	vpn := &mobileVPN{}
	start := time.Now()

	// 第一次采样只落基准点，速率无从计算。
	if _, _, down, up := vpn.sampleTraffic(start); down != 0 || up != 0 {
		t.Fatalf("first sample speeds = %d/%d, want 0/0", down, up)
	}

	vpn.bytesIn.Add(4096)
	vpn.bytesOut.Add(2048)
	in, out, down, up := vpn.sampleTraffic(start.Add(2 * time.Second))
	if in != 4096 || out != 2048 {
		t.Fatalf("totals = %d/%d, want 4096/2048", in, out)
	}
	if down != 2048 || up != 1024 {
		t.Fatalf("speeds = %d/%d, want 2048/1024", down, up)
	}

	// 间隔过短时沿用上一轮速率，别让几毫秒的差值把数字放大几个数量级。
	vpn.bytesIn.Add(1)
	if _, _, down, up = vpn.sampleTraffic(start.Add(2*time.Second + 10*time.Millisecond)); down != 2048 || up != 1024 {
		t.Fatalf("speeds after short interval = %d/%d, want 2048/1024", down, up)
	}

	// 空闲区间速率归零，累计量继续保留。
	in, out, down, up = vpn.sampleTraffic(start.Add(4 * time.Second))
	if down != 0 || up != 0 {
		t.Fatalf("idle speeds = %d/%d, want 0/0", down, up)
	}
	if in != 4097 || out != 2048 {
		t.Fatalf("totals = %d/%d, want 4097/2048", in, out)
	}
}
