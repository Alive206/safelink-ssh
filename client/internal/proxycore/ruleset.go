package proxycore

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/example/safelink/pkg/config"
)

// 内置 geosite/geoip 规则集。以前这三个规则集是 remote 类型、启动时现下载，
// 结果分流规则本身成了能上网的前提：拉不到就直接 FATAL 起不来。
// 内置之后启动零网络依赖，规则集永远在，更新退化成后台的事。
//
//go:embed rulesets/*.srs
var embeddedRuleSets embed.FS

// embeddedRuleSetFiles 把 rule_set tag 映射到内置文件名。geosite-geolocation-!cn
// 的 tag 带 '!'，直接当文件名在各种工具链和 shell 里都容易出问题，所以另起名字。
var embeddedRuleSetFiles = map[string]string{
	config.ProxyRuleSetGeoIPCN:                 "geoip-cn.srs",
	config.ProxyRuleSetGeositeGeolocationCN:    "geosite-geolocation-cn.srs",
	config.ProxyRuleSetGeositeGeolocationNotCN: "geosite-geolocation-not-cn.srs",
}

const (
	// 规则集本地文件超过这个时长就后台更新一次
	ruleSetRefreshInterval = 24 * time.Hour

	// 下载回来的 .srs 不应该比这还小，小于此值基本是错误页而不是规则集
	minRuleSetBytes = 1024
)

// ensureLocalRuleSetFiles 把内置规则集铺到 dir，返回 tag -> 本地文件路径。
// 已经存在且大小一致的文件不重写：批量测速会反复调用，每次重写几百 KB 没必要。
func ensureLocalRuleSetFiles(dir string, tags []string) (map[string]string, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	paths := make(map[string]string, len(tags))
	created := false
	for _, tag := range tags {
		filename, ok := embeddedRuleSetFiles[tag]
		if !ok {
			continue
		}
		embedded, err := embeddedRuleSets.ReadFile("rulesets/" + filename)
		if err != nil {
			return nil, fmt.Errorf("read embedded rule set %s: %w", tag, err)
		}
		path := filepath.Join(dir, filename)
		if info, err := os.Stat(path); err == nil && info.Size() == int64(len(embedded)) {
			paths[tag] = path
			continue
		}
		if !created {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create rule set dir: %w", err)
			}
			created = true
		}
		if err := writeFileAtomic(path, embedded); err != nil {
			return nil, fmt.Errorf("write rule set %s: %w", tag, err)
		}
		paths[tag] = path
	}
	return paths, nil
}

// RefreshRuleSets 触发一次规则集更新，供包外调用。桌面端在 Runner.Start 成功后
// 自己会调；Android 的 sing-box 进程由 Kotlin 侧启动、不经过 Runner，只能显式调这里，
// 否则规则集会永远停在编译时那一份快照。
func RefreshRuleSets(ctx context.Context, dir string) {
	refreshLocalRuleSets(ctx, dir)
}

// refreshLocalRuleSets 把过期的本地规则集更新一遍。只在代理已经起来之后调用，
// 失败就继续用旧文件：规则旧一点无所谓，但绝不能因为更新失败影响分流。
// sing-box 的 local rule_set 不会热重载，新规则下次启动生效。
func refreshLocalRuleSets(ctx context.Context, dir string) {
	client := &http.Client{
		Timeout: 30 * time.Second,
		// 显式不走代理：更新规则集不该依赖代理是否正常
		Transport: &http.Transport{Proxy: nil},
	}
	for tag, filename := range embeddedRuleSetFiles {
		if ctx.Err() != nil {
			return
		}
		url, ok := remoteProxyRuleSetURLs[tag]
		if !ok {
			continue
		}
		path := filepath.Join(dir, filename)
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < ruleSetRefreshInterval {
			continue
		}
		data, err := downloadRuleSet(ctx, client, url)
		if err != nil {
			continue
		}
		// 原子替换，避免 sing-box 读到写了一半的文件
		_ = writeFileAtomic(path, data)
	}
}

func downloadRuleSet(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SafeLink/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	// 限一下上限，别被异常响应拖爆内存
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, err
	}
	if len(data) < minRuleSetBytes {
		return nil, fmt.Errorf("rule set too small: %d bytes", len(data))
	}
	// 二进制 .srs 不会以 '<' 开头，这样能挡掉 HTML 错误页
	if data[0] == '<' {
		return nil, errors.New("response is not a binary rule set")
	}
	return data, nil
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Windows 上 rename 到已存在的文件会失败，先删旧的
	_ = os.Remove(path)
	return os.Rename(tmpName, path)
}
