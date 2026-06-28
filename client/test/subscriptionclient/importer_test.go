package subscriptionclient_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/safelink/client/internal/manager"
	"github.com/example/safelink/client/internal/store"
	"github.com/example/safelink/client/internal/subscriptionclient"
	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/proxysubscription"
	"github.com/example/safelink/pkg/subscription"
)

func TestImportFetchesAndImportsVPNTunnels(t *testing.T) {
	body, err := subscription.EncodeSafeLinkJSON([]config.TunnelCfg{{
		Name:    "hk",
		Mode:    config.ModeVPN,
		Forward: "vpn.example.com:1562",
		SSH: config.SSHCfg{
			User:     "admin",
			Password: "secret",
		},
		Tun: config.TunCfg{
			Subnet: "10.8.0.2/24",
			DNS:    []string{"1.1.1.1"},
		},
	}})
	if err != nil {
		t.Fatalf("EncodeSafeLinkJSON: %v", err)
	}

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(httpSrv.Close)

	mgr, st := newTestManager(t)
	if _, err := subscriptionclient.Import(context.Background(), mgr, st, "office", httpSrv.URL); err != nil {
		t.Fatalf("Import returned error: %v", err)
	}

	subs, err := st.LoadSubscriptions()
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("len(subs) = %d, want 1", len(subs))
	}
	if subs[0].Format != subscription.FormatSafeLinkJSON || subs[0].TunnelCount != 1 || subs[0].LastRefresh == "" {
		t.Fatalf("subscription metadata not updated: %+v", subs[0])
	}

	statuses := mgr.List()
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if statuses[0].Config.Name != "office-hk" || statuses[0].Config.Mode != config.ModeVPN {
		t.Fatalf("unexpected imported tunnel: %+v", statuses[0].Config)
	}
}

func TestImportFetchesAndStoresProxyNodes(t *testing.T) {
	body := []byte(`
proxies:
  - name: ss-hk
    type: ss
    server: ss.example.com
    port: 8388
    cipher: aes-128-gcm
    password: secret
`)

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(httpSrv.Close)

	mgr, st := newTestManager(t)
	result, err := subscriptionclient.Import(context.Background(), mgr, st, "airport", httpSrv.URL)
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}
	if result.Kind != store.SubscriptionKindProxy || result.Imported != 1 {
		t.Fatalf("unexpected import result: %+v", result)
	}

	nodes, err := st.LoadProxyNodes()
	if err != nil {
		t.Fatalf("LoadProxyNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].Name != "airport-ss-hk" || nodes[0].Protocol != proxysubscription.ProtocolShadowsocks {
		t.Fatalf("unexpected proxy node: %+v", nodes[0])
	}

	statuses := mgr.List()
	if len(statuses) != 0 {
		t.Fatalf("proxy import should not create VPN tunnels: %+v", statuses)
	}
}

func newTestManager(t *testing.T) (*manager.Manager, *store.Store) {
	t.Helper()

	st := store.New(t.TempDir())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := manager.New(nil, config.ConnDefaults{}, log, st)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr.Start(ctx)
	return mgr, st
}
