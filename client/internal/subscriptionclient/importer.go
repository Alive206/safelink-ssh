// Package subscriptionclient imports remote VPN subscriptions into the client manager.
package subscriptionclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/example/safelink/client/internal/manager"
	"github.com/example/safelink/client/internal/store"
	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/proxysubscription"
	"github.com/example/safelink/pkg/subscription"
)

// Usage is the Clash-compatible subscription-userinfo metadata returned by a provider.
type Usage struct {
	Upload   uint64
	Download uint64
	Total    uint64
	Expire   int64
}

const subscriptionUserAgent = "ClashVerge/2.4.1"

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

	data, usage, err := FetchWithUsage(ctx, url)
	if err != nil {
		return Result{}, err
	}

	tunnels, detected, err := subscription.Parse(data, subscription.FormatAuto)
	if err == nil {
		return importVPNTunnels(mgr, st, name, url, tunnels, detected, usage)
	}
	nodes, proxyDetected, proxyErr := proxysubscription.Parse(data, proxysubscription.FormatAuto)
	if proxyErr == nil {
		return importProxyNodes(st, name, url, nodes, proxyDetected, usage)
	}
	return Result{}, fmt.Errorf("parse subscription: %v; parse proxy subscription: %w", err, proxyErr)
}

func Refresh(ctx context.Context, mgr *manager.Manager, st *store.Store, id string) (Result, error) {
	sources, err := st.LoadSubscriptions()
	if err != nil {
		return Result{}, err
	}
	for _, src := range sources {
		if src.ID == id {
			return refreshSource(ctx, mgr, st, src)
		}
	}
	return Result{}, fmt.Errorf("subscription %q not found", id)
}

func RefreshAll(ctx context.Context, mgr *manager.Manager, st *store.Store) (imported, skipped int, errs []string) {
	sources, err := st.LoadSubscriptions()
	if err != nil {
		return 0, 0, []string{err.Error()}
	}
	for _, src := range sources {
		result, err := refreshSource(ctx, mgr, st, src)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", src.Name, err))
			skipped++
			continue
		}
		imported += result.Imported
		skipped += result.Skipped
		errs = append(errs, result.Errors...)
	}
	return imported, skipped, compactErrors(errs)
}

func Update(ctx context.Context, mgr *manager.Manager, st *store.Store, id, name, url string, autoRefresh bool, intervalMin int) (Result, error) {
	name = strings.TrimSpace(name)
	url = strings.TrimSpace(url)
	if name == "" {
		return Result{}, fmt.Errorf("subscription name is required")
	}
	if url == "" {
		return Result{}, fmt.Errorf("subscription URL is required")
	}
	sources, err := st.LoadSubscriptions()
	if err != nil {
		return Result{}, err
	}
	for _, src := range sources {
		if src.ID == id {
			src.Name = name
			src.URL = url
			src.AutoRefresh = autoRefresh
			src.IntervalMin = intervalMin
			if err := st.UpdateSubscription(src); err != nil {
				return Result{}, err
			}
			return refreshSource(ctx, mgr, st, src)
		}
	}
	return Result{}, fmt.Errorf("subscription %q not found", id)
}

func importVPNTunnels(mgr *manager.Manager, st *store.Store, name, url string, tunnels []config.TunnelCfg, detected string, usage Usage) (Result, error) {
	imported, skipped, errs := mgr.BulkUpsert(tunnels, Prefix(name))

	src := store.SubscriptionSource{
		ID:          store.NewID(),
		Name:        name,
		URL:         url,
		Format:      detected,
		Kind:        store.SubscriptionKindVPN,
		Enabled:     true,
		LastRefresh: time.Now().Format(time.RFC3339),
		LastError:   strings.Join(errs, "; "),
		TunnelCount: imported,
		UsageUpload: usage.Upload, UsageDownload: usage.Download, UsageTotal: usage.Total, UsageExpire: usage.Expire,
	}
	if err := st.AddSubscription(src); err != nil {
		return Result{}, err
	}
	if imported == 0 && len(errs) > 0 {
		return Result{}, errors.New(src.LastError)
	}
	return Result{Source: src, Kind: store.SubscriptionKindVPN, Imported: imported, Skipped: skipped, Errors: errs}, nil
}

func importProxyNodes(st *store.Store, name, url string, nodes []proxysubscription.ProxyNode, detected string, usage Usage) (Result, error) {
	sourceID := store.NewID()
	prefix := Prefix(name)
	filtered := nodes[:0]
	for i := range nodes {
		if proxysubscription.IsInformationalNodeName(nodes[i].Name) {
			continue
		}
		nodes[i].Name = prefix + nodes[i].Name
		nodes[i].SubscriptionID = sourceID
		filtered = append(filtered, nodes[i])
	}
	imported, skipped, errs := st.UpsertProxyNodes(filtered)
	src := store.SubscriptionSource{
		ID:          sourceID,
		Name:        name,
		URL:         url,
		Format:      detected,
		Kind:        store.SubscriptionKindProxy,
		Enabled:     true,
		LastRefresh: time.Now().Format(time.RFC3339),
		LastError:   strings.Join(errs, "; "),
		NodeCount:   imported,
		UsageUpload: usage.Upload, UsageDownload: usage.Download, UsageTotal: usage.Total, UsageExpire: usage.Expire,
	}
	if err := st.AddSubscription(src); err != nil {
		return Result{}, err
	}
	if imported == 0 && len(errs) > 0 {
		return Result{}, errors.New(src.LastError)
	}
	return Result{Source: src, Kind: store.SubscriptionKindProxy, Imported: imported, Skipped: skipped, Errors: errs}, nil
}

func refreshSource(ctx context.Context, mgr *manager.Manager, st *store.Store, src store.SubscriptionSource) (Result, error) {
	data, usage, err := FetchWithUsage(ctx, src.URL)
	if err != nil {
		src.LastRefresh = time.Now().Format(time.RFC3339)
		src.LastError = err.Error()
		_ = st.UpdateSubscription(src)
		return Result{}, err
	}

	tunnels, detected, err := subscription.Parse(data, subscription.FormatAuto)
	if err == nil {
		return refreshVPNTunnels(mgr, st, src, tunnels, detected, usage)
	}
	nodes, proxyDetected, proxyErr := proxysubscription.Parse(data, proxysubscription.FormatAuto)
	if proxyErr == nil {
		return refreshProxyNodes(st, src, nodes, proxyDetected, usage)
	}
	parseErr := fmt.Errorf("parse subscription: %v; parse proxy subscription: %w", err, proxyErr)
	src.LastRefresh = time.Now().Format(time.RFC3339)
	src.LastError = parseErr.Error()
	_ = st.UpdateSubscription(src)
	return Result{}, parseErr
}

func refreshVPNTunnels(mgr *manager.Manager, st *store.Store, src store.SubscriptionSource, tunnels []config.TunnelCfg, detected string, usage Usage) (Result, error) {
	imported, skipped, errs := mgr.BulkUpsert(tunnels, Prefix(src.Name))
	src.Format = detected
	src.Kind = store.SubscriptionKindVPN
	src.LastRefresh = time.Now().Format(time.RFC3339)
	src.LastError = strings.Join(errs, "; ")
	src.TunnelCount = imported
	src.NodeCount = 0
	src.UsageUpload, src.UsageDownload, src.UsageTotal, src.UsageExpire = usage.Upload, usage.Download, usage.Total, usage.Expire
	if err := st.UpdateSubscription(src); err != nil {
		return Result{}, err
	}
	if imported == 0 && len(errs) > 0 {
		return Result{}, errors.New(src.LastError)
	}
	return Result{Source: src, Kind: store.SubscriptionKindVPN, Imported: imported, Skipped: skipped, Errors: errs}, nil
}

func refreshProxyNodes(st *store.Store, src store.SubscriptionSource, nodes []proxysubscription.ProxyNode, detected string, usage Usage) (Result, error) {
	existing, err := st.LoadProxyNodes()
	if err != nil {
		return Result{}, err
	}
	existingIDs := make(map[string]string)
	for _, node := range existing {
		if node.SubscriptionID == src.ID {
			existingIDs[node.Name] = node.ID
		}
	}
	if err := st.DeleteProxyNodesBySubscriptionID(src.ID); err != nil {
		return Result{}, err
	}
	prefix := Prefix(src.Name)
	filtered := nodes[:0]
	for i := range nodes {
		if proxysubscription.IsInformationalNodeName(nodes[i].Name) {
			continue
		}
		nodes[i].Name = prefix + nodes[i].Name
		nodes[i].SubscriptionID = src.ID
		nodes[i].ID = existingIDs[nodes[i].Name]
		filtered = append(filtered, nodes[i])
	}
	imported, skipped, errs := st.UpsertProxyNodes(filtered)
	src.Format = detected
	src.Kind = store.SubscriptionKindProxy
	src.LastRefresh = time.Now().Format(time.RFC3339)
	src.LastError = strings.Join(errs, "; ")
	src.TunnelCount = 0
	src.NodeCount = imported
	src.UsageUpload, src.UsageDownload, src.UsageTotal, src.UsageExpire = usage.Upload, usage.Download, usage.Total, usage.Expire
	if err := st.UpdateSubscription(src); err != nil {
		return Result{}, err
	}
	if imported == 0 && len(errs) > 0 {
		return Result{}, errors.New(src.LastError)
	}
	return Result{Source: src, Kind: store.SubscriptionKindProxy, Imported: imported, Skipped: skipped, Errors: errs}, nil
}

func compactErrors(errs []string) []string {
	result := make([]string, 0, len(errs))
	for _, item := range errs {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
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
	data, _, err := FetchWithUsage(ctx, url)
	return data, err
}

func FetchWithUsage(ctx context.Context, url string) ([]byte, Usage, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, Usage{}, fmt.Errorf("create subscription request: %w", err)
	}
	// Some subscription providers only expose Clash-compatible usage metadata to known clients.
	req.Header.Set("User-Agent", subscriptionUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, Usage{}, fmt.Errorf("fetch subscription: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, Usage{}, fmt.Errorf("fetch subscription: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, Usage{}, fmt.Errorf("read subscription: %w", err)
	}
	return data, parseUsage(resp.Header.Get("subscription-userinfo")), nil
}

func parseUsage(raw string) Usage {
	var usage Usage
	for _, field := range strings.Split(raw, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case "upload":
			usage.Upload = n
		case "download":
			usage.Download = n
		case "total":
			usage.Total = n
		case "expire":
			usage.Expire = int64(n)
		}
	}
	return usage
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
