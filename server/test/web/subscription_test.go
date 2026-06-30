package web_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/safelink/server/internal/web"
)

func TestSubscriptionEndpointRequiresToken(t *testing.T) {
	srv := web.NewWithOptions("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), web.Options{
		Subscription: web.SubscriptionConfig{
			Name:       "demo",
			PublicAddr: "vpn.example.com:1562",
			Username:   "admin",
			Password:   "secret",
			Token:      "token-1",
			Subnet:     "10.8.0.2/24",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/subscription?format=json", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestSubscriptionEndpointServesSafeLinkJSON(t *testing.T) {
	srv := testSubscriptionServer()
	req := httptest.NewRequest(http.MethodGet, "/api/subscription?format=json&token=token-1", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	body := rec.Body.String()
	for _, want := range []string{`"version": 1`, `"name": "demo"`, `"forward": "vpn.example.com:1562"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestSubscriptionEndpointServesClashYAML(t *testing.T) {
	srv := testSubscriptionServer()
	req := httptest.NewRequest(http.MethodGet, "/api/subscription?format=clash&token=token-1", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/x-yaml" {
		t.Fatalf("Content-Type = %q", got)
	}
	body := rec.Body.String()
	for _, want := range []string{"type: safelink-vpn", "server: vpn.example.com", "port: 1562"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func testSubscriptionServer() *web.Server {
	return web.NewWithOptions("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), web.Options{
		Subscription: web.SubscriptionConfig{
			Name:       "demo",
			PublicAddr: "vpn.example.com:1562",
			Username:   "admin",
			Password:   "secret",
			Token:      "token-1",
			Subnet:     "10.8.0.2/24",
			DNS:        []string{"1.1.1.1"},
			AutoRoute:  true,
			SNI:        "front.example.com",
		},
	})
}
