// Package subscriptionclient imports remote VPN subscriptions into the client manager.
package subscriptionclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/example/safelink/client/internal/manager"
	"github.com/example/safelink/client/internal/store"
	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/proxysubscription"
	"github.com/example/safelink/pkg/subscription"
)

type Result struct {
	Source   store.SubscriptionSource
	Kind     string
	Imported int
	Skipped  int
	Errors   []string
}

func Import(ctx context.Context, mgr *manager.Manager, st *store.Store, name, url string) (Result, error) {
	name = strings.TrimSpace(name)
	url = strings.TrimSpace(url)
	if name == "" {
		return Result{}, fmt.Errorf("subscription name is required")
	}
	if url == "" {
		return Result{}, fmt.Errorf("subscription URL is required")
	}

	data, err := FetchRaw(ctx, url)
	if err != nil {
		return Result{}, err
	}

	tunnels, detected, err := subscription.Parse(data, subscription.FormatAuto)
	if err == nil {
		return importVPNTunnels(mgr, st, name, url, tunnels, detected)
	}
	nodes, proxyDetected, proxyErr := proxysubscription.Parse(data, proxysubscription.FormatAuto)
	if proxyErr == nil {
		return importProxyNodes(st, name, url, nodes, proxyDetected)
	}
	return Result{}, fmt.Errorf("parse subscription: %v; parse proxy subscription: %w", err, proxyErr)
}

func importVPNTunnels(mgr *manager.Manager, st *store.Store, name, url string, tunnels []config.TunnelCfg, detected string) (Result, error) {
	imported, skipped, errs := mgr.BulkUpsert(tunnels, Prefix(name))

	src := store.SubscriptionSource{
		ID:          store.NewID(),
		Name:        name,
		URL:         url,
		Format:      detected,
		Kind:        store.SubscriptionKindVPN,
		LastRefresh: time.Now().Format(time.RFC3339),
		LastError:   strings.Join(errs, "; "),
		TunnelCount: imported,
	}
	if err := st.AddSubscription(src); err != nil {
		return Result{}, err
	}
	if imported == 0 && len(errs) > 0 {
		return Result{}, errors.New(src.LastError)
	}
	return Result{Source: src, Kind: store.SubscriptionKindVPN, Imported: imported, Skipped: skipped, Errors: errs}, nil
}

func importProxyNodes(st *store.Store, name, url string, nodes []proxysubscription.ProxyNode, detected string) (Result, error) {
	sourceID := store.NewID()
	prefix := Prefix(name)
	for i := range nodes {
		nodes[i].Name = prefix + nodes[i].Name
		nodes[i].SubscriptionID = sourceID
	}
	imported, skipped, errs := st.UpsertProxyNodes(nodes)
	src := store.SubscriptionSource{
		ID:          sourceID,
		Name:        name,
		URL:         url,
		Format:      detected,
		Kind:        store.SubscriptionKindProxy,
		LastRefresh: time.Now().Format(time.RFC3339),
		LastError:   strings.Join(errs, "; "),
		NodeCount:   imported,
	}
	if err := st.AddSubscription(src); err != nil {
		return Result{}, err
	}
	if imported == 0 && len(errs) > 0 {
		return Result{}, errors.New(src.LastError)
	}
	return Result{Source: src, Kind: store.SubscriptionKindProxy, Imported: imported, Skipped: skipped, Errors: errs}, nil
}

func Fetch(ctx context.Context, url, format string) ([]config.TunnelCfg, string, error) {
	data, err := FetchRaw(ctx, url)
	if err != nil {
		return nil, "", err
	}
	tunnels, detected, err := subscription.Parse(data, format)
	if err != nil {
		return nil, "", fmt.Errorf("parse subscription: %w", err)
	}
	return tunnels, detected, nil
}

func FetchRaw(ctx context.Context, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create subscription request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch subscription: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch subscription: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read subscription: %w", err)
	}
	return data, nil
}

func Prefix(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	prefix := strings.Trim(b.String(), "-")
	if prefix == "" {
		prefix = "subscription"
	}
	return prefix + "-"
}
