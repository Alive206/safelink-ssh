package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/example/sshtunneld/internal/config"
	"github.com/example/sshtunneld/internal/logging"
	"github.com/example/sshtunneld/internal/manager"
)

// handler bundles every dependency the HTTP routes need.
type handler struct {
	mgr   *manager.Manager
	bcast *logging.Broadcaster
	auth  *authenticator
	log   *slog.Logger
}

func newHandler(mgr *manager.Manager, bcast *logging.Broadcaster, auth *authenticator, log *slog.Logger) *handler {
	return &handler{mgr: mgr, bcast: bcast, auth: auth, log: log.With("component", "web")}
}

// routes registers every endpoint on the given mux.  Auth is applied at the
// route level so /api/login and the static UI can be public.
func (h *handler) routes(mux *http.ServeMux) {
	// Public.
	mux.HandleFunc("POST /api/login", h.login)
	mux.HandleFunc("POST /api/logout", h.logout)
	mux.HandleFunc("GET /api/auth-info", h.authInfo)

	// Protected JSON API.
	mux.Handle("GET /api/tunnels", h.protect(h.listTunnels))
	mux.Handle("POST /api/tunnels", h.protect(h.createTunnel))
	mux.Handle("GET /api/tunnels/{name}", h.protect(h.getTunnel))
	mux.Handle("PUT /api/tunnels/{name}", h.protect(h.updateTunnel))
	mux.Handle("DELETE /api/tunnels/{name}", h.protect(h.deleteTunnel))
	mux.Handle("POST /api/tunnels/{name}/start", h.protect(h.startTunnel))
	mux.Handle("POST /api/tunnels/{name}/stop", h.protect(h.stopTunnel))
	mux.Handle("POST /api/tunnels/{name}/restart", h.protect(h.restartTunnel))
	mux.Handle("GET /api/stats", h.protect(h.allStats))
	mux.Handle("GET /api/stats/{name}", h.protect(h.oneStats))
	mux.Handle("GET /api/logs", h.protect(sseLogs(h.bcast)))

	// SSH key management.
	mux.Handle("GET /api/keys", h.protect(h.listKeys))
	mux.Handle("POST /api/keys", h.protect(h.uploadKey))
	mux.Handle("DELETE /api/keys/{name}", h.protect(h.deleteKey))

	// Static UI.
	mux.Handle("GET /", uiHandler())
}

// protect wraps fn with the authenticator.  /api endpoints reply with 401
// JSON; the UI handler is unprotected so the SPA can self-render the login
// page even when the user has no session yet.
func (h *handler) protect(fn http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.auth.validate(r) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		fn(w, r)
	})
}

// ----- auth endpoints -----------------------------------------------------

func (h *handler) authInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"auth_required": h.auth.authEnabled(),
		"login_enabled": h.auth.hasUsers(),
	})
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !h.auth.hasUsers() {
		writeErr(w, http.StatusServiceUnavailable, "no users configured")
		return
	}
	tok := h.auth.login(req.Username, req.Password)
	if tok == "" {
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		h.auth.logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ----- tunnel endpoints ---------------------------------------------------

func (h *handler) listTunnels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.mgr.List())
}

func (h *handler) getTunnel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	st, err := h.mgr.Get(name)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *handler) createTunnel(w http.ResponseWriter, r *http.Request) {
	var tc config.TunnelCfg
	if err := json.NewDecoder(r.Body).Decode(&tc); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := h.mgr.Add(tc); err != nil {
		mapErr(w, err)
		return
	}
	st, _ := h.mgr.Get(tc.Name)
	writeJSON(w, http.StatusCreated, st)
}

func (h *handler) updateTunnel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var tc config.TunnelCfg
	if err := json.NewDecoder(r.Body).Decode(&tc); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := h.mgr.Update(name, tc); err != nil {
		mapErr(w, err)
		return
	}
	st, _ := h.mgr.Get(name)
	writeJSON(w, http.StatusOK, st)
}

func (h *handler) deleteTunnel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := h.mgr.Delete(name); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) startTunnel(w http.ResponseWriter, r *http.Request) {
	if err := h.mgr.StartTunnel(r.PathValue("name")); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *handler) stopTunnel(w http.ResponseWriter, r *http.Request) {
	if err := h.mgr.Stop(r.PathValue("name")); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *handler) restartTunnel(w http.ResponseWriter, r *http.Request) {
	if err := h.mgr.Restart(r.PathValue("name")); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *handler) allStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.mgr.Stats())
}

func (h *handler) oneStats(w http.ResponseWriter, r *http.Request) {
	st, err := h.mgr.StatsOf(r.PathValue("name"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// ----- helpers ------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// mapErr translates a manager-level error into the appropriate HTTP code.
func mapErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, manager.ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, manager.ErrExists):
		writeErr(w, http.StatusConflict, err.Error())
	case strings.Contains(err.Error(), "rename via update"):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeErr(w, http.StatusBadRequest, err.Error())
	}
}
