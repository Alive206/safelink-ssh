package web_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/server/internal/web"
	"golang.org/x/crypto/bcrypt"
)

func TestLoginUsesConfiguredBcryptUser(t *testing.T) {
	srv := testAuthServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Result().Cookies(); len(got) == 0 {
		t.Fatalf("expected login cookie")
	}
}

func TestProtectedAPIRequiresLogin(t *testing.T) {
	srv := testAuthServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestProtectedAPIAcceptsSessionCookie(t *testing.T) {
	srv := testAuthServer(t)
	login := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	login.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(loginRec, login)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	for _, cookie := range loginRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSubscriptionWithoutTokenRequiresLoginWhenAuthEnabled(t *testing.T) {
	srv := web.NewWithOptions("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), web.Options{
		Auth: config.AuthCfg{APIToken: "api-token"},
		Subscription: web.SubscriptionConfig{
			Name:       "demo",
			PublicAddr: "vpn.example.com:1562",
			Username:   "admin",
			Password:   "secret",
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

func testAuthServer(t *testing.T) *web.Server {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return web.NewWithOptions("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), web.Options{
		Auth: config.AuthCfg{
			Users: []config.UserCfg{
				{Username: "admin", PasswordHash: string(hash)},
			},
		},
	})
}
