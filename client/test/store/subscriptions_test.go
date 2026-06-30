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
