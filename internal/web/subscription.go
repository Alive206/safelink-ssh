package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/example/sshtunneld/internal/config"
	"github.com/example/sshtunneld/internal/store"
)

// ----- Publish endpoints (server mode) ------------------------------------

// subPublish handles GET /sub/{token} — public, token is auth.
func (h *handler) subPublish(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")

	stored, err := h.mgr.Store().LoadSubToken()
	if err != nil || stored == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(stored)) != 1 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}

	tunnels := h.mgr.Configs()
	secrets := r.URL.Query().Get("secrets") == "true"

	// Strip sensitive fields by default.
	if !secrets {
		for i := range tunnels {
			tunnels[i].SSH.Password = ""
			tunnels[i].SSH.Passphrase = ""
		}
	}

	format := r.URL.Query().Get("format")
	switch format {
	case "clash":
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Content-Disposition", "inline; filename=\"clash.yaml\"")
		writeClashYAML(w, tunnels)
	default: // "json" or empty
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", "inline; filename=\"safelink.json\"")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(tunnels)
	}
}

// getSubToken handles GET /api/subscription/token — returns current token + URL.
func (h *handler) getSubToken(w http.ResponseWriter, r *http.Request) {
	tok, err := h.mgr.Store().LoadSubToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	url := buildSubURL(r, tok)
	writeJSON(w, http.StatusOK, map[string]any{
		"token": tok,
		"url":   url,
	})
}

// regenerateSubToken handles POST /api/subscription/token/regenerate.
func (h *handler) regenerateSubToken(w http.ResponseWriter, r *http.Request) {
	tok, err := h.mgr.Store().RegenerateSubToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	url := buildSubURL(r, tok)
	writeJSON(w, http.StatusOK, map[string]any{
		"token": tok,
		"url":   url,
	})
}

// getSubNodes handles GET /api/subscription/nodes — returns published nodes preview.
func (h *handler) getSubNodes(w http.ResponseWriter, _ *http.Request) {
	tunnels := h.mgr.Configs()
	writeJSON(w, http.StatusOK, toNodeInfos(tunnels))
}

func buildSubURL(r *http.Request, token string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		scheme = fwd
	}
	host := r.Host
	return fmt.Sprintf("%s://%s/sub/%s", scheme, host, token)
}

// ----- Import endpoints (client mode) -------------------------------------

// listSubscriptions handles GET /api/subscription/imports.
func (h *handler) listSubscriptions(w http.ResponseWriter, _ *http.Request) {
	sources, err := h.mgr.Store().LoadSubscriptions()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sources)
}

// addSubscription handles POST /api/subscription/imports.
func (h *handler) addSubscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		URL         string `json:"url"`
		Format      string `json:"format"`
		AutoRefresh bool   `json:"auto_refresh"`
		IntervalMin int    `json:"interval_min"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Name == "" || req.URL == "" {
		writeErr(w, http.StatusBadRequest, "name and url are required")
		return
	}
	if req.Format == "" {
		req.Format = "auto"
	}
	if req.IntervalMin <= 0 {
		req.IntervalMin = 60
	}

	sources, err := h.mgr.Store().LoadSubscriptions()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	src := store.SubscriptionSource{
		ID:          randomSubID(),
		Name:        req.Name,
		URL:         req.URL,
		Format:      req.Format,
		AutoRefresh: req.AutoRefresh,
		IntervalMin: req.IntervalMin,
	}
	sources = append(sources, src)
	if err := h.mgr.Store().SaveSubscriptions(sources); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, src)
}

// deleteSubscription handles DELETE /api/subscription/imports/{id}.
func (h *handler) deleteSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sources, err := h.mgr.Store().LoadSubscriptions()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	found := false
	result := make([]store.SubscriptionSource, 0, len(sources))
	for _, s := range sources {
		if s.ID == id {
			found = true
			continue
		}
		result = append(result, s)
	}
	if !found {
		writeErr(w, http.StatusNotFound, "subscription source not found")
		return
	}
	if err := h.mgr.Store().SaveSubscriptions(result); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// subImportRefresh handles POST /api/subscription/imports/{id}/refresh.
func (h *handler) subImportRefresh(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sources, err := h.mgr.Store().LoadSubscriptions()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	var src *store.SubscriptionSource
	var idx int
	for i := range sources {
		if sources[i].ID == id {
			src = &sources[i]
			idx = i
			break
		}
	}
	if src == nil {
		writeErr(w, http.StatusNotFound, "subscription source not found")
		return
	}

	imported, skipped, errs, nodes := h.doRefresh(src)

	sources[idx] = *src
	_ = h.mgr.Store().SaveSubscriptions(sources)

	writeJSON(w, http.StatusOK, map[string]any{
		"imported": imported,
		"skipped":  skipped,
		"errors":   errs,
		"nodes":    nodes,
	})
}

// nodeInfo is a simplified node representation returned to the frontend.
type nodeInfo struct {
	Name    string `json:"name"`
	Mode    string `json:"mode"`
	Address string `json:"address"`
}

func toNodeInfos(tunnels []config.TunnelCfg) []nodeInfo {
	nodes := make([]nodeInfo, 0, len(tunnels))
	for _, t := range tunnels {
		addr := t.SSH.Addr
		if t.Mode == config.ModeVPN {
			addr = t.Forward // VPN uses forward as server addr
		}
		if addr == "" {
			addr = t.Listen
		}
		nodes = append(nodes, nodeInfo{Name: t.Name, Mode: t.Mode, Address: addr})
	}
	return nodes
}

// doRefresh fetches and merges a single subscription source.
func (h *handler) doRefresh(src *store.SubscriptionSource) (imported, skipped int, errs []string, nodes []nodeInfo) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(src.URL)
	if err != nil {
		src.LastError = err.Error()
		src.LastRefresh = time.Now().Format(time.RFC3339)
		return 0, 0, []string{err.Error()}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("remote returned %d", resp.StatusCode)
		src.LastError = msg
		src.LastRefresh = time.Now().Format(time.RFC3339)
		return 0, 0, []string{msg}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MB max
	if err != nil {
		src.LastError = err.Error()
		src.LastRefresh = time.Now().Format(time.RFC3339)
		return 0, 0, []string{err.Error()}, nil
	}

	tunnels, err := parseSubscription(body, src.Format)
	if err != nil {
		src.LastError = err.Error()
		src.LastRefresh = time.Now().Format(time.RFC3339)
		return 0, 0, []string{err.Error()}, nil
	}

	nodes = toNodeInfos(tunnels)

	// Strip sensitive fields from imported tunnels? No — the remote decides what to send.
	imported, skipped, errs = h.mgr.BulkMerge(tunnels, "")

	src.LastRefresh = time.Now().Format(time.RFC3339)
	src.LastError = ""
	src.TunnelCount = len(tunnels)
	if len(errs) > 0 {
		src.LastError = errs[0]
	}
	return
}

// ----- Auto-refresh loop --------------------------------------------------

// subscriptionRefreshLoop runs in a background goroutine, checking all
// auto_refresh sources and refreshing them when their interval elapses.
func (h *handler) subscriptionRefreshLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sources, err := h.mgr.Store().LoadSubscriptions()
			if err != nil {
				continue
			}
			changed := false
			for i := range sources {
				if !sources[i].AutoRefresh {
					continue
				}
				last, _ := time.Parse(time.RFC3339, sources[i].LastRefresh)
				interval := time.Duration(sources[i].IntervalMin) * time.Minute
				if interval <= 0 {
					interval = 60 * time.Minute
				}
				if time.Since(last) < interval {
					continue
				}
				h.doRefresh(&sources[i])
				changed = true
			}
			if changed {
				_ = h.mgr.Store().SaveSubscriptions(sources)
			}
		}
	}
}

// randomSubID generates a short random ID for subscription sources.
func randomSubID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "sub_" + hex.EncodeToString(b)
}
