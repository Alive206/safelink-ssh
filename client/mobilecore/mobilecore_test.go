package mobilecore

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// rule 模式下配置里要带 rule_set，而规则集文件必须铺在应用私有目录里。
// 之前这条路径没覆盖：只测了 global 模式，而 global 不碰 rule_set，
// 结果“目录回落到 os.TempDir() 导致 Android 上铺不出去”这个问题完全没被拦住。
func TestCoreBuildsRuleModeConfigWithLocalRuleSetsUnderDataDir(t *testing.T) {
	dataDir := t.TempDir()
	core := NewCore(dataDir)
	payload := `{"mode":"rule","node":{"name":"ss","protocol":"ss","server":"example.com","port":8388,"method":"aes-128-gcm","password":"secret"}}`

	var resp struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
		Data    struct {
			Config string `json:"config"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(core.Call("buildProxyConfig", payload)), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("buildProxyConfig failed: %s", resp.Message)
	}

	var cfg struct {
		Route struct {
			RuleSet []struct {
				Type string `json:"type"`
				Tag  string `json:"tag"`
				Path string `json:"path"`
				URL  string `json:"url"`
			} `json:"rule_set"`
		} `json:"route"`
		Experimental struct {
			CacheFile struct {
				Path string `json:"path"`
			} `json:"cache_file"`
		} `json:"experimental"`
	}
	if err := json.Unmarshal([]byte(resp.Data.Config), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Route.RuleSet) == 0 {
		t.Fatal("rule 模式的配置里没有 rule_set")
	}
	for _, rs := range cfg.Route.RuleSet {
		if rs.Type != "local" {
			t.Fatalf("rule_set %s type = %s, want local", rs.Tag, rs.Type)
		}
		if rs.URL != "" {
			t.Fatalf("rule_set %s 还带着 url=%s，启动又会去联网", rs.Tag, rs.URL)
		}
		// 必须在应用私有目录下：Android 上 os.TempDir() 是 /data/local/tmp，写不进去
		if !strings.HasPrefix(rs.Path, dataDir) {
			t.Fatalf("rule_set %s path = %s, want under %s", rs.Tag, rs.Path, dataDir)
		}
		info, err := os.Stat(rs.Path)
		if err != nil {
			t.Fatalf("rule_set %s 文件没落盘: %v", rs.Tag, err)
		}
		if info.Size() == 0 {
			t.Fatalf("rule_set %s 文件是空的", rs.Tag)
		}
	}
	// cache_file 同样不能落到内核写不进去的地方，否则 .srs 缓存等于白做
	if !strings.HasPrefix(cfg.Experimental.CacheFile.Path, dataDir) {
		t.Fatalf("cache_file path = %s, want under %s", cfg.Experimental.CacheFile.Path, dataDir)
	}
}

// Kotlin 侧会把目录注入到 payload 里，注入的值要能盖掉默认值。
func TestCoreHonorsInjectedRuleSetDir(t *testing.T) {
	core := NewCore(t.TempDir())
	ruleSetDir := filepath.Join(t.TempDir(), "injected-rule-sets")
	payload := `{"mode":"rule","rule_set_dir":` + quoteJSON(ruleSetDir) + `,` +
		`"node":{"name":"ss","protocol":"ss","server":"example.com","port":8388,"method":"aes-128-gcm","password":"secret"}}`

	var resp struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
		Data    struct {
			Config string `json:"config"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(core.Call("buildProxyConfig", payload)), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("buildProxyConfig failed: %s", resp.Message)
	}
	if !strings.Contains(resp.Data.Config, strings.ReplaceAll(ruleSetDir, `\`, `\\`)) {
		t.Fatalf("注入的 rule_set_dir 没生效，config = %s", resp.Data.Config)
	}
	if entries, err := os.ReadDir(ruleSetDir); err != nil || len(entries) == 0 {
		t.Fatalf("规则集没铺到注入的目录: %v", err)
	}
}

func quoteJSON(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(encoded)
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
