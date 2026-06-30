package sysproxy

import "testing"

func TestBuildProxyServerPrefersSettingsFriendlyHTTPProxy(t *testing.T) {
	got := buildProxyServer("127.0.0.1:10809", "127.0.0.1:10808")
	if got != "127.0.0.1:10809" {
		t.Fatalf("buildProxyServer() = %q, want settings-friendly HTTP endpoint", got)
	}
}

func TestBuildProxyServerFallsBackToSocksOnly(t *testing.T) {
	got := buildProxyServer("", "127.0.0.1:10808")
	if got != "socks=127.0.0.1:10808" {
		t.Fatalf("buildProxyServer() = %q, want socks fallback", got)
	}
}
