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

func TestSPAFallbackServesIndexForClientRoutes(t *testing.T) {
	srv := web.NewWithOptions("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), web.Options{})
	req := httptest.NewRequest(http.MethodGet, "/clients/active", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "SafeLink") {
		t.Fatalf("expected SPA index, got %q", rec.Body.String())
	}
}
