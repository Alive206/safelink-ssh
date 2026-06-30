package store_test

import (
	"testing"

	"github.com/example/safelink/client/internal/store"
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
