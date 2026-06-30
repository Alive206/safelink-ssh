package web_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/server/internal/vpnserver"
	"github.com/example/safelink/server/internal/web"
)

func TestStatusIncludesRuntimeAndSubscriptionState(t *testing.T) {
	srv := testAPIServer()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer api-token")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Service string `json:"service"`
		Runtime struct {
			ListenAddr    string `json:"listen_addr"`
			ActiveClients int    `json:"active_clients"`
		} `json:"runtime"`
		Subscription struct {
			Enabled bool `json:"enabled"`
		} `json:"subscription"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Service != "safelink-server" || body.Runtime.ListenAddr != ":1562" || !body.Subscription.Enabled {
		t.Fatalf("body = %#v", body)
	}
}

func TestRuntimeClientsEndpointReturnsActiveClients(t *testing.T) {
	rt := vpnserver.NewRuntime(vpnserver.RuntimeConfig{ListenAddr: ":1562"})
	id := rt.RegisterClient(&net.UDPAddr{IP: net.ParseIP("203.0.113.8"), Port: 4444})
	rt.AddClientTraffic(id, 100, 200)
	srv := newAPIServer(rt)
	req := httptest.NewRequest(http.MethodGet, "/api/runtime/clients", nil)
	req.Header.Set("Authorization", "Bearer api-token")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Clients []vpnserver.ClientSnapshot `json:"clients"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Clients) != 1 || body.Clients[0].RemoteAddr != "203.0.113.8:4444" || body.Clients[0].BytesIn != 100 {
		t.Fatalf("clients = %#v", body.Clients)
	}
}

func TestSubscriptionInfoEndpoint(t *testing.T) {
	srv := testAPIServer()
	req := httptest.NewRequest(http.MethodGet, "/api/subscription/info", nil)
	req.Header.Set("Authorization", "Bearer api-token")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Enabled bool   `json:"enabled"`
		JSONURL string `json:"json_url"`
		YAMLURL string `json:"yaml_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Enabled || body.JSONURL == "" || body.YAMLURL == "" {
		t.Fatalf("body = %#v", body)
	}
}

func TestVPNServersEndpointReturnsCurrentNode(t *testing.T) {
	srv := testAPIServer()
	req := httptest.NewRequest(http.MethodGet, "/api/vpn/servers", nil)
	req.Header.Set("Authorization", "Bearer api-token")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var nodes []config.TunnelCfg
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Forward != "vpn.example.com:1562" {
		t.Fatalf("nodes = %#v", nodes)
	}
}

func testAPIServer() *web.Server {
	return newAPIServer(vpnserver.NewRuntime(vpnserver.RuntimeConfig{
		ListenAddr: ":1562",
		Subnet:     "10.0.8.0/24",
		NATIface:   "eth0",
		NATEnabled: true,
		Padding:    true,
	}))
}

func newAPIServer(rt *vpnserver.Runtime) *web.Server {
	return web.NewWithOptions("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), web.Options{
		Auth: config.AuthCfg{APIToken: "api-token"},
		Subscription: web.SubscriptionConfig{
			Name:       "demo",
			PublicAddr: "vpn.example.com:1562",
			Username:   "vpn",
			Password:   "secret",
			Token:      "sub-token",
			Subnet:     "10.8.0.2/24",
			AutoRoute:  true,
		},
		Runtime: rt,
	})
}
