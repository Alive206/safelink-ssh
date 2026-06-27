package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/example/sshtunneld/internal/config"
	"github.com/example/sshtunneld/internal/deploy"
	"github.com/example/sshtunneld/internal/logging"
	"github.com/example/sshtunneld/internal/manager"
	"github.com/example/sshtunneld/internal/store"
	"github.com/example/sshtunneld/internal/tunnel"
)

// handler bundles every dependency the HTTP routes need.
type handler struct {
	mgr        *manager.Manager
	bcast      *logging.Broadcaster
	auth       *authenticator
	log        *slog.Logger
	role       string // "server" | "client" | "standalone"
	shutdownFn func() // called to initiate graceful daemon shutdown
}

func newHandler(mgr *manager.Manager, bcast *logging.Broadcaster, auth *authenticator, log *slog.Logger, role string) *handler {
	return &handler{mgr: mgr, bcast: bcast, auth: auth, log: log.With("component", "web"), role: role}
}

// routes registers every endpoint on the given mux.  Auth is applied at the
// route level so /api/login and the static UI can be public.
func (h *handler) routes(mux *http.ServeMux) {
	// Public.
	mux.HandleFunc("POST /api/login", h.login)
	mux.HandleFunc("POST /api/logout", h.logout)
	mux.HandleFunc("GET /api/auth-info", h.authInfo)
	mux.HandleFunc("GET /api/role", h.getRole)

	// Protected JSON API — shared (all roles).
	mux.Handle("GET /api/tunnels", h.protect(h.listTunnels))
	mux.Handle("POST /api/tunnels", h.protect(h.createTunnel))
	mux.Handle("GET /api/tunnels/{name}", h.protect(h.getTunnel))
	mux.Handle("PUT /api/tunnels/{name}", h.protect(h.updateTunnel))
	mux.Handle("DELETE /api/tunnels/{name}", h.protect(h.deleteTunnel))
	mux.Handle("POST /api/tunnels/{name}/start", h.protect(h.startTunnel))
	mux.Handle("POST /api/tunnels/{name}/stop", h.protect(h.stopTunnel))
	mux.Handle("POST /api/tunnels/{name}/restart", h.protect(h.restartTunnel))
	mux.Handle("POST /api/tunnels/{name}/route", h.protect(h.toggleRoute))
	mux.Handle("GET /api/stats", h.protect(h.allStats))
	mux.Handle("GET /api/stats/{name}", h.protect(h.oneStats))
	mux.Handle("GET /api/logs", h.protect(sseLogs(h.bcast)))

	// SSH key management.
	mux.Handle("GET /api/keys", h.protect(h.listKeys))
	mux.Handle("POST /api/keys", h.protect(h.uploadKey))
	mux.Handle("DELETE /api/keys/{name}", h.protect(h.deleteKey))

	// Server-only routes (server + standalone).
	if h.role == config.RoleServer || h.role == config.RoleStandalone {
		mux.Handle("POST /api/vpn/deploy", h.protect(h.vpnDeploy))
		mux.Handle("GET /api/vpn/servers", h.protect(h.listVPNServers))
		mux.Handle("POST /api/vpn/servers", h.protect(h.addVPNServer))
		mux.Handle("DELETE /api/vpn/servers/{id}", h.protect(h.deleteVPNServer))
		mux.HandleFunc("GET /sub/{token}", h.subPublish)
		mux.Handle("GET /api/subscription/token", h.protect(h.getSubToken))
		mux.Handle("POST /api/subscription/token/regenerate", h.protect(h.regenerateSubToken))
		mux.Handle("GET /api/subscription/nodes", h.protect(h.getSubNodes))
	}

	// Client-only routes (client + standalone).
	if h.role == config.RoleClient || h.role == config.RoleStandalone {
		mux.Handle("GET /api/vpn/driver", h.protect(h.vpnDriverCheck))
		mux.Handle("POST /api/vpn/driver/install", h.protect(h.vpnDriverInstall))
		mux.Handle("GET /api/subscription/imports", h.protect(h.listSubscriptions))
		mux.Handle("POST /api/subscription/imports", h.protect(h.addSubscription))
		mux.Handle("DELETE /api/subscription/imports/{id}", h.protect(h.deleteSubscription))
		mux.Handle("POST /api/subscription/imports/{id}/refresh", h.protect(h.subImportRefresh))
	}

	// Static UI.
	mux.Handle("GET /", uiHandler())

	// Internal: shutdown (localhost only, no auth needed).
	mux.HandleFunc("POST /api/shutdown", h.shutdownHandler)
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

// getRole returns the configured application role for the frontend to adapt its UI.
func (h *handler) getRole(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"role": h.role})
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

func (h *handler) toggleRoute(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := h.mgr.SetVPNRoute(name, req.Enable); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "route_active": req.Enable})
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

// vpnDriverCheck reports TUN driver status.
func (h *handler) vpnDriverCheck(w http.ResponseWriter, _ *http.Request) {
	st, _ := tunnel.CheckDriver()
	writeJSON(w, http.StatusOK, st)
}

// vpnDriverInstall downloads and installs Wintun on Windows.
func (h *handler) vpnDriverInstall(w http.ResponseWriter, _ *http.Request) {
	if err := tunnel.InstallDriver(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	st, _ := tunnel.CheckDriver()
	writeJSON(w, http.StatusOK, st)
}

// vpnDeploy deploys a VPN server to a remote VPS via SSH and creates a
// local VPN tunnel configuration.
func (h *handler) vpnDeploy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SSH       config.SSHCfg `json:"ssh"`
		Subnet    string        `json:"subnet"`
		VPNUser   string        `json:"vpn_user"`
		VPNPass   string        `json:"vpn_pass"`
		LocalName string        `json:"local_name"`
		Force     bool          `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	params := deploy.DeployParams{
		SSH:        req.SSH,
		Subnet:     req.Subnet,
		VPNUser:    req.VPNUser,
		VPNPass:    req.VPNPass,
		ServerPort: "1562",
		Force:      req.Force,
	}

	result, err := deploy.DeployVPNServer(params, h.log)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Automatically create a local VPN tunnel configuration.
	if req.LocalName == "" {
		req.LocalName = "vpn-" + result.ServerAddr
	}
	tc := deploy.CreateTunnelCfg(result, req.LocalName)
	if err := h.mgr.Add(tc); err != nil {
		// Tunnel creation is best-effort; deployment already succeeded.
		h.log.Warn("create local tunnel after deploy", "err", err)
	} else {
		result.TunnelName = tc.Name
		h.log.Info("local VPN tunnel created", "name", tc.Name)
	}

	// Save the deployed server to vpn_servers.json for future reuse.
	srv := store.VPNServer{
		Name:        req.LocalName,
		ServerAddr:  result.ServerAddr,
		ServerPort:  result.ServerPort,
		Subnet:      result.Subnet,
		VPNUser:     result.VPNUser,
		VPNPass:     result.VPNPass,
		SSHAddr:     req.SSH.Addr,
		SSHUser:     req.SSH.User,
		SSHPassword: req.SSH.Password,
		EgressIface: result.EgressIface,
		Status:      result.Status,
	}
	if _, err := h.mgr.Store().AddServer(srv); err != nil {
		h.log.Warn("save vpn server record", "err", err)
	}

	writeJSON(w, http.StatusOK, result)
}

// ----- VPN server list endpoints -----

func (h *handler) listVPNServers(w http.ResponseWriter, _ *http.Request) {
	servers, err := h.mgr.Store().LoadServers()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, servers)
}

func (h *handler) addVPNServer(w http.ResponseWriter, r *http.Request) {
	var srv store.VPNServer
	if err := json.NewDecoder(r.Body).Decode(&srv); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if srv.ServerAddr == "" {
		writeErr(w, http.StatusBadRequest, "server_addr is required")
		return
	}
	if srv.Name == "" {
		srv.Name = "vpn-" + srv.ServerAddr
	}
	if srv.ServerPort == "" {
		srv.ServerPort = "1562"
	}
	if srv.Status == "" {
		srv.Status = "manual"
	}
	saved, err := h.mgr.Store().AddServer(srv)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (h *handler) deleteVPNServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.mgr.Store().DeleteServer(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErr(w, http.StatusNotFound, err.Error())
		} else {
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

// shutdownHandler allows the tray program to gracefully stop the daemon.
// Only accepts requests from localhost.
func (h *handler) shutdownHandler(w http.ResponseWriter, r *http.Request) {
	// Only allow from localhost.
	remote := r.RemoteAddr
	if !strings.HasPrefix(remote, "127.0.0.1:") && !strings.HasPrefix(remote, "[::1]:") {
		writeErr(w, http.StatusForbidden, "shutdown only allowed from localhost")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "msg": "shutting down"})
	if h.shutdownFn != nil {
		go func() {
			time.Sleep(200 * time.Millisecond) // let response flush
			h.shutdownFn()
		}()
	}
}
