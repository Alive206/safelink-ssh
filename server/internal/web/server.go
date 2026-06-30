package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/subscription"
	"github.com/example/safelink/server/internal/vpnserver"
)

// Server wraps the HTTP server for the web control panel.
type Server struct {
	httpSrv *http.Server
	log     *slog.Logger
}

type Options struct {
	Subscription SubscriptionConfig
	Auth         config.AuthCfg
	Runtime      *vpnserver.Runtime
}

type SubscriptionConfig struct {
	Name       string
	PublicAddr string
	Username   string
	Password   string
	Token      string
	Subnet     string
	DNS        []string
	AutoRoute  bool
	SNI        string
	PinSHA256  string
	Padding    *bool
}

// New creates a new web server listening on addr.
func New(addr string, log *slog.Logger) *Server {
	return NewWithOptions(addr, log, Options{})
}

// NewWithOptions creates a web server with optional feature handlers.
func NewWithOptions(addr string, log *slog.Logger, opts Options) *Server {
	mux := http.NewServeMux()
	auth := newAuthService(opts.Auth)

	// API routes
	mux.HandleFunc("POST /api/auth/login", auth.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", auth.handleLogout)
	mux.HandleFunc("GET /api/auth/me", auth.handleMe)
	mux.HandleFunc("GET /api/status", auth.require(handleStatus(opts)))
	mux.HandleFunc("GET /api/runtime/clients", auth.require(handleRuntimeClients(opts)))
	mux.HandleFunc("GET /api/subscription/info", auth.require(handleSubscriptionInfo(opts.Subscription)))
	subscriptionHandler := handleSubscription(opts.Subscription)
	if opts.Subscription.Token == "" {
		subscriptionHandler = auth.require(subscriptionHandler)
	}
	mux.HandleFunc("GET /api/subscription", subscriptionHandler)
	mux.HandleFunc("GET /subscription", subscriptionHandler)
	mux.HandleFunc("GET /api/vpn/servers", auth.require(handleListServers(opts.Subscription)))
	mux.HandleFunc("POST /api/vpn/servers", auth.require(handleAddServer))
	mux.HandleFunc("DELETE /api/vpn/servers/{id}", auth.require(handleDeleteServer))

	// Serve frontend SPA
	mux.Handle("/", spaHandler())

	return &Server{
		httpSrv: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
		log: log,
	}
}

// Start begins serving HTTP in a goroutine. Call Shutdown to stop.
func (s *Server) Start() {
	go func() {
		s.log.Info("web panel starting", "addr", s.httpSrv.Addr)
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("web server error", "err", err)
		}
	}()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.httpSrv.Shutdown(ctx)
}

// Handler returns the configured HTTP handler for tests and embedders.
func (s *Server) Handler() http.Handler {
	return s.httpSrv.Handler
}

// --- API handlers ---

func handleStatus(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":       "ok",
			"service":      "safelink-server",
			"runtime":      runtimeSnapshot(opts.Runtime),
			"subscription": subscriptionSummary(r, opts.Subscription),
		})
	}
}

func handleRuntimeClients(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot := runtimeSnapshot(opts.Runtime)
		writeJSON(w, http.StatusOK, map[string]any{
			"clients": snapshot.Clients,
			"summary": snapshot,
		})
	}
}

func handleSubscriptionInfo(cfg SubscriptionConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, subscriptionSummary(r, cfg))
	}
}

func handleListServers(cfg SubscriptionConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tunnel, err := cfg.Tunnel()
		if err != nil {
			writeJSON(w, http.StatusOK, []config.TunnelCfg{})
			return
		}
		writeJSON(w, http.StatusOK, []config.TunnelCfg{tunnel})
	}
}

func handleAddServer(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "server node is managed by server config", http.StatusMethodNotAllowed)
}

func handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "server node is managed by server config", http.StatusMethodNotAllowed)
}

func handleSubscription(cfg SubscriptionConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Token != "" && !subscriptionTokenOK(r, cfg.Token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		tunnel, err := cfg.Tunnel()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}

		format := r.URL.Query().Get("format")
		if format == "" {
			format = subscription.FormatSafeLinkJSON
		}

		var out []byte
		contentType := "application/json"
		switch format {
		case "json", subscription.FormatSafeLinkJSON:
			out, err = subscription.EncodeSafeLinkJSON([]config.TunnelCfg{tunnel})
		case "clash", "yaml", "yml", subscription.FormatClashYAML:
			out, err = subscription.EncodeClashYAML([]config.TunnelCfg{tunnel})
			contentType = "application/x-yaml"
		default:
			http.Error(w, fmt.Sprintf("unsupported format %q", format), http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(out)
	}
}

func subscriptionTokenOK(r *http.Request, want string) bool {
	if r.URL.Query().Get("token") == want {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+want
}

func (cfg SubscriptionConfig) Tunnel() (config.TunnelCfg, error) {
	if cfg.Name == "" {
		cfg.Name = "safelink-vpn"
	}
	if cfg.PublicAddr == "" {
		return config.TunnelCfg{}, fmt.Errorf("subscription public address is not configured")
	}
	tunnel := config.TunnelCfg{
		Name:    cfg.Name,
		Mode:    config.ModeVPN,
		Forward: cfg.PublicAddr,
		SSH: config.SSHCfg{
			User:     cfg.Username,
			Password: cfg.Password,
		},
		Tun: config.TunCfg{
			Subnet:    cfg.Subnet,
			DNS:       cfg.DNS,
			AutoRoute: cfg.AutoRoute,
			SNI:       cfg.SNI,
			PinSHA256: cfg.PinSHA256,
			Padding:   cfg.Padding,
		},
	}
	if tunnel.Tun.Subnet == "" {
		tunnel.Tun.Subnet = "10.8.0.2/24"
	}
	if err := config.ValidateTunnel(tunnel); err != nil {
		return config.TunnelCfg{}, err
	}
	return tunnel, nil
}

func runtimeSnapshot(rt *vpnserver.Runtime) vpnserver.RuntimeSnapshot {
	if rt == nil {
		return vpnserver.RuntimeSnapshot{Clients: []vpnserver.ClientSnapshot{}}
	}
	snapshot := rt.Snapshot()
	if snapshot.Clients == nil {
		snapshot.Clients = []vpnserver.ClientSnapshot{}
	}
	return snapshot
}

func subscriptionSummary(r *http.Request, cfg SubscriptionConfig) map[string]any {
	enabled := cfg.PublicAddr != ""
	base := publicBaseURL(r)
	jsonURL := base + "/api/subscription?format=json"
	yamlURL := base + "/api/subscription?format=clash"
	if cfg.Token != "" {
		jsonURL += "&token=" + cfg.Token
		yamlURL += "&token=" + cfg.Token
	}
	return map[string]any{
		"enabled":       enabled,
		"name":          cfg.Name,
		"public_addr":   cfg.PublicAddr,
		"token_enabled": cfg.Token != "",
		"json_url":      jsonURL,
		"yaml_url":      yamlURL,
		"subnet":        cfg.Subnet,
		"auto_route":    cfg.AutoRoute,
	}
}

func publicBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "127.0.0.1"
	}
	return scheme + "://" + host
}
