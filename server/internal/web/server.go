package web

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Server wraps the HTTP server for the web control panel.
type Server struct {
	httpSrv *http.Server
	log     *slog.Logger
}

// New creates a new web server listening on addr.
func New(addr string, log *slog.Logger) *Server {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("GET /api/status", handleStatus)
	mux.HandleFunc("GET /api/vpn/servers", handleListServers)
	mux.HandleFunc("POST /api/vpn/servers", handleAddServer)
	mux.HandleFunc("DELETE /api/vpn/servers/{id}", handleDeleteServer)

	// Serve frontend SPA
	fileServer := http.FileServer(Assets())
	mux.Handle("/", fileServer)

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

// --- API handlers ---

func handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok","service":"safelink-server"}`))
}

func handleListServers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`[]`))
}

func handleAddServer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"id":"placeholder"}`))
}

func handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
