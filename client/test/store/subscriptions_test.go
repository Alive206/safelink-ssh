package store_test

import (
	"testing"

	"github.com/example/safelink/client/internal/store"
	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/proxysubscription"
)

func TestLoadActiveProxyNodesOnlyReturnsEnabledSubscriptionNodes(t *testing.T) {
	st := store.New(t.TempDir())
	disabled := store.SubscriptionSource{ID: "sub-disabled", Name: "disabled", Kind: store.SubscriptionKindProxy, Enabled: false}
	enabled := store.SubscriptionSource{ID: "sub-enabled", Name: "enabled", Kind: store.SubscriptionKindProxy, Enabled: true}
	if err := st.SaveSubscriptions([]store.SubscriptionSource{disabled, enabled}); err != nil {
		t.Fatalf("SaveSubscriptions: %v", err)
	}
	if err := st.SaveProxyNodes([]proxysubscription.ProxyNode{
		{Name: "disabled-node", Protocol: proxysubscription.ProtocolShadowsocks, Server: "disabled.example.com", Port: 8388, Password: "secret", SubscriptionID: disabled.ID},
		{Name: "enabled-node", Protocol: proxysubscription.ProtocolShadowsocks, Server: "enabled.example.com", Port: 8388, Password: "secret", SubscriptionID: enabled.ID},
		{Name: "剩余流量：142.45 GB", Protocol: proxysubscription.ProtocolShadowsocks, Server: "info.example.com", Port: 8388, Password: "secret", SubscriptionID: enabled.ID},
	}); err != nil {
		t.Fatalf("SaveProxyNodes: %v", err)
	}

	nodes, err := st.LoadActiveProxyNodes()
	if err != nil {
		t.Fatalf("LoadActiveProxyNodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "enabled-node" {
		t.Fatalf("active nodes = %+v, want only enabled-node", nodes)
	}

	if _, err := st.SetSubscriptionEnabled(enabled.ID, false); err != nil {
		t.Fatalf("SetSubscriptionEnabled: %v", err)
	}
	nodes, err = st.LoadActiveProxyNodes()
	if err != nil {
		t.Fatalf("LoadActiveProxyNodes after disable: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("active nodes after disabling all = %+v, want empty", nodes)
	}
}

func TestSetSubscriptionEnabledDisablesOtherProxySubscriptions(t *testing.T) {
	st := store.New(t.TempDir())
	first := store.SubscriptionSource{ID: "sub-first", Name: "first", Kind: store.SubscriptionKindProxy, Enabled: true}
	second := store.SubscriptionSource{ID: "sub-second", Name: "second", Kind: store.SubscriptionKindProxy, Enabled: false}
	if err := st.SaveSubscriptions([]store.SubscriptionSource{first, second}); err != nil {
		t.Fatalf("SaveSubscriptions: %v", err)
	}
	if err := st.SaveProxyNodes([]proxysubscription.ProxyNode{
		{Name: "first-node", Protocol: proxysubscription.ProtocolShadowsocks, Server: "first.example.com", Port: 8388, Password: "secret", SubscriptionID: first.ID},
		{Name: "second-node", Protocol: proxysubscription.ProtocolShadowsocks, Server: "second.example.com", Port: 8388, Password: "secret", SubscriptionID: second.ID},
	}); err != nil {
		t.Fatalf("SaveProxyNodes: %v", err)
	}

	if _, err := st.SetSubscriptionEnabled(second.ID, true); err != nil {
		t.Fatalf("SetSubscriptionEnabled: %v", err)
	}
	sources, err := st.LoadSubscriptions()
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v", err)
	}
	for _, src := range sources {
		want := src.ID == second.ID
		if src.Enabled != want {
			t.Fatalf("subscription %q enabled = %v, want %v", src.ID, src.Enabled, want)
		}
	}
	nodes, err := st.LoadActiveProxyNodes()
	if err != nil {
		t.Fatalf("LoadActiveProxyNodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "second-node" {
		t.Fatalf("active nodes = %+v, want only second-node", nodes)
	}
}

func TestSetSubscriptionEnabledKeepsOtherKindsEnabled(t *testing.T) {
	st := store.New(t.TempDir())
	vpn := store.SubscriptionSource{ID: "sub-vpn", Name: "vpn", Kind: store.SubscriptionKindVPN, Enabled: true}
	proxy := store.SubscriptionSource{ID: "sub-proxy", Name: "proxy", Kind: store.SubscriptionKindProxy, Enabled: false}
	if err := st.SaveSubscriptions([]store.SubscriptionSource{vpn, proxy}); err != nil {
		t.Fatalf("SaveSubscriptions: %v", err)
	}

	if _, err := st.SetSubscriptionEnabled(proxy.ID, true); err != nil {
		t.Fatalf("SetSubscriptionEnabled: %v", err)
	}
	sources, err := st.LoadSubscriptions()
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v", err)
	}
	for _, src := range sources {
		if !src.Enabled {
			t.Fatalf("subscription %q disabled, want both kinds enabled: %+v", src.ID, sources)
		}
	}
}

func TestAddSubscriptionStaysDisabledWhenAnotherIsEnabled(t *testing.T) {
	st := store.New(t.TempDir())
	if err := st.AddSubscription(store.SubscriptionSource{ID: "sub-first", Name: "first", Kind: store.SubscriptionKindProxy, Enabled: true}); err != nil {
		t.Fatalf("AddSubscription first: %v", err)
	}
	if err := st.AddSubscription(store.SubscriptionSource{ID: "sub-second", Name: "second", Kind: store.SubscriptionKindProxy, Enabled: true}); err != nil {
		t.Fatalf("AddSubscription second: %v", err)
	}
	sources, err := st.LoadSubscriptions()
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %+v, want two", sources)
	}
	if !sources[0].Enabled || sources[1].Enabled {
		t.Fatalf("enabled flags = %v/%v, want only the first source enabled", sources[0].Enabled, sources[1].Enabled)
	}
}

func TestNormalizeEnabledSubscriptionsKeepsFirstPerKind(t *testing.T) {
	st := store.New(t.TempDir())
	if err := st.SaveSubscriptions([]store.SubscriptionSource{
		{ID: "sub-a", Name: "a", Kind: store.SubscriptionKindProxy, Enabled: true},
		{ID: "sub-b", Name: "b", Kind: store.SubscriptionKindProxy, Enabled: true},
		{ID: "sub-c", Name: "c", Kind: store.SubscriptionKindVPN, Enabled: true},
	}); err != nil {
		t.Fatalf("SaveSubscriptions: %v", err)
	}

	if err := st.NormalizeEnabledSubscriptions(); err != nil {
		t.Fatalf("NormalizeEnabledSubscriptions: %v", err)
	}
	sources, err := st.LoadSubscriptions()
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v", err)
	}
	enabled := map[string]bool{}
	for _, src := range sources {
		enabled[src.ID] = src.Enabled
	}
	if !enabled["sub-a"] || enabled["sub-b"] || !enabled["sub-c"] {
		t.Fatalf("enabled state = %v, want sub-a and sub-c only", enabled)
	}
}

func TestSettingsMigrationAddsRuleSetsOnce(t *testing.T) {
	st := store.New(t.TempDir())
	oldRules := []config.ProxyRule{
		{ID: "lan-ipv4-private-10", Name: "IPv4 私有地址", Type: config.ProxyRuleTypeIPCIDR, Value: "10.0.0.0/8", Outbound: config.ProxyRuleOutboundDirect, Enabled: true},
	}
	if err := st.SaveSettings(store.ClientSettings{RuleModeRules: oldRules}); err != nil {
		t.Fatalf("SaveSettings old rules: %v", err)
	}
	settings, err := st.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings migrated: %v", err)
	}
	if !hasRuleSet(settings.RuleModeRules, config.ProxyRuleSetGeositeGeolocationCN) ||
		!hasRuleSet(settings.RuleModeRules, config.ProxyRuleSetGeoIPCN) ||
		!hasRuleSet(settings.RuleModeRules, config.ProxyRuleSetGeositeGeolocationNotCN) {
		t.Fatalf("migrated rules missing default rule sets: %+v", settings.RuleModeRules)
	}

	settings.RuleModeRules = oldRules
	if err := st.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings after manual deletion: %v", err)
	}
	settings, err = st.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings after manual deletion: %v", err)
	}
	if hasAnyRuleSet(settings.RuleModeRules) {
		t.Fatalf("rule sets should not be re-added after versioned manual deletion: %+v", settings.RuleModeRules)
	}
}

func TestLoadActiveProxyNodesDoesNotTreatSubscriptionPrefixAsInformation(t *testing.T) {
	st := store.New(t.TempDir())
	src := store.SubscriptionSource{ID: "sub-traffic", Name: "流量订阅", Kind: store.SubscriptionKindProxy, Enabled: true}
	if err := st.SaveSubscriptions([]store.SubscriptionSource{src}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveProxyNodes([]proxysubscription.ProxyNode{{Name: "subscription-node", Protocol: proxysubscription.ProtocolShadowsocks, Server: "node.example.com", Port: 8388, Method: "aes-128-gcm", Password: "secret", SubscriptionID: src.ID}}); err != nil {
		t.Fatal(err)
	}
	nodes, err := st.LoadActiveProxyNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %+v, want one normal node", nodes)
	}
}

func TestSettingsPersistSelectedProxyNode(t *testing.T) {
	st := store.New(t.TempDir())
	want := store.ClientSettings{SelectedProxyNodeID: "node-1", SelectedProxySubscriptionID: "sub-1"}
	if err := st.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.SelectedProxyNodeID != want.SelectedProxyNodeID || got.SelectedProxySubscriptionID != want.SelectedProxySubscriptionID {
		t.Fatalf("selected node = %q/%q, want %q/%q", got.SelectedProxyNodeID, got.SelectedProxySubscriptionID, want.SelectedProxyNodeID, want.SelectedProxySubscriptionID)
	}
}

func hasRuleSet(rules []config.ProxyRule, value string) bool {
	for _, rule := range rules {
		if rule.Type == config.ProxyRuleTypeRuleSet && rule.Value == value {
			return true
		}
	}
	return false
}

func hasAnyRuleSet(rules []config.ProxyRule) bool {
	for _, rule := range rules {
		if rule.Type == config.ProxyRuleTypeRuleSet {
			return true
		}
	}
	return false
}

// 同为 VLESS 时按传输层变体排：REALITY + vision 比 WebSocket 少一层 HTTP upgrade
// 和帧开销，默认顺序要把它们放前面。
func TestLoadActiveProxyNodesPrefersRealityVisionOverWebSocket(t *testing.T) {
	st := store.New(t.TempDir())
	sub := store.SubscriptionSource{ID: "sub", Name: "sub", Kind: store.SubscriptionKindProxy, Enabled: true}
	if err := st.SaveSubscriptions([]store.SubscriptionSource{sub}); err != nil {
		t.Fatalf("SaveSubscriptions: %v", err)
	}

	ws := proxysubscription.ProxyNode{
		Name: "vless-ws-tls", Protocol: proxysubscription.ProtocolVLESS,
		Server: "ws.example.com", Port: 443, UUID: "11111111-1111-1111-1111-111111111111",
		SubscriptionID: sub.ID,
		Transport:      proxysubscription.TransportOptions{Type: "ws", Path: "/path"},
		TLS:            &proxysubscription.TLSOptions{Enabled: true, ServerName: "ws.example.com"},
	}
	realityVision := proxysubscription.ProxyNode{
		Name: "vless-reality-vision", Protocol: proxysubscription.ProtocolVLESS,
		Server: "reality.example.com", Port: 443, UUID: "11111111-1111-1111-1111-111111111111",
		SubscriptionID: sub.ID, Flow: "xtls-rprx-vision",
		TLS:            &proxysubscription.TLSOptions{Enabled: true, ServerName: "reality.example.com", PublicKey: "pubkey", ShortID: "beef"},
	}
	// 存入时故意把 WS 排在前面，确保顺序是排序逻辑得出的而不是写入顺序
	if err := st.SaveProxyNodes([]proxysubscription.ProxyNode{ws, realityVision}); err != nil {
		t.Fatalf("SaveProxyNodes: %v", err)
	}

	nodes, err := st.LoadActiveProxyNodes()
	if err != nil {
		t.Fatalf("LoadActiveProxyNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("loaded %d nodes, want 2", len(nodes))
	}
	if nodes[0].Name != "vless-reality-vision" {
		t.Fatalf("first node = %q, want vless-reality-vision", nodes[0].Name)
	}
}
