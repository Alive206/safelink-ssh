package proxycore_test

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/safelink/client/internal/proxycore"
	"github.com/example/safelink/client/internal/store"
)

// TestRealSubscriptionBatchLatency 用本机真实订阅跑一次批量测速，用来量 TestNodes 的
// 墙钟耗时和成功率。默认 skip，需要设置 SAFELINK_REAL_NODES_DIR 指向客户端数据目录
// （即 %APPDATA%\SafeLink），并且能找到真实的 sing-box 可执行文件。
func TestRealSubscriptionBatchLatency(t *testing.T) {
	dataDir := os.Getenv("SAFELINK_REAL_NODES_DIR")
	if dataDir == "" {
		t.Skip("set SAFELINK_REAL_NODES_DIR to the client data dir to run against a real subscription")
	}
	corePath := os.Getenv("SAFELINK_SINGBOX")
	if corePath == "" {
		corePath = proxycore.FindCorePath("")
	}
	if corePath == "" {
		t.Skip("no sing-box executable found; set SAFELINK_SINGBOX")
	}

	nodes, err := store.New(dataDir).LoadActiveProxyNodes()
	if err != nil {
		t.Fatalf("LoadActiveProxyNodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Skip("no active proxy nodes in store")
	}

	timeout := 5 * time.Second
	if raw := os.Getenv("SAFELINK_TEST_TIMEOUT_MS"); raw != "" {
		ms, convErr := strconv.Atoi(raw)
		if convErr != nil {
			t.Fatalf("parse SAFELINK_TEST_TIMEOUT_MS: %v", convErr)
		}
		timeout = time.Duration(ms) * time.Millisecond
	}
	rounds := 1
	if raw := os.Getenv("SAFELINK_TEST_ROUNDS"); raw != "" {
		n, convErr := strconv.Atoi(raw)
		if convErr != nil {
			t.Fatalf("parse SAFELINK_TEST_ROUNDS: %v", convErr)
		}
		rounds = n
	}

	t.Logf("core=%s nodes=%d timeout=%s rounds=%d", corePath, len(nodes), timeout, rounds)

	for round := 1; round <= rounds; round++ {
		var firstResultAt time.Duration
		start := time.Now()
		results := proxycore.TestNodes(t.Context(), nodes, proxycore.Options{
			CorePath: corePath,
			WorkDir:  filepath.Join(t.TempDir(), "work"),
		}, timeout, func(proxycore.TestResult) {
			if firstResultAt == 0 {
				firstResultAt = time.Since(start)
			}
		})
		elapsed := time.Since(start)

		latencies := make([]int, 0, len(results))
		errCounts := map[string]int{}
		failProtocols := map[string]int{}
		for i, result := range results {
			if result.OK {
				latencies = append(latencies, int(result.LatencyMS))
				continue
			}
			errCounts[classifyError(result.Error)]++
			failProtocols[nodes[i].Protocol]++
			if os.Getenv("SAFELINK_TEST_RAW_ERRORS") != "" {
				t.Logf("round %d: raw fail proto=%s err=%s", round, nodes[i].Protocol, result.Error)
			}
		}
		sort.Ints(latencies)

		t.Logf("round %d: elapsed=%s first_result=%s ok=%d/%d",
			round, elapsed.Round(time.Millisecond), firstResultAt.Round(time.Millisecond),
			len(latencies), len(results))
		if len(latencies) > 0 {
			t.Logf("round %d: latency min=%dms p50=%dms p90=%dms max=%dms",
				round, latencies[0], latencies[len(latencies)/2],
				latencies[min(len(latencies)*9/10, len(latencies)-1)], latencies[len(latencies)-1])
		}
		for kind, count := range errCounts {
			t.Logf("round %d: fail %-28s x%d", round, kind, count)
		}
		for proto, count := range failProtocols {
			t.Logf("round %d: fail protocol %-12s x%d", round, proto, count)
		}
	}
}

// classifyError 把错误归成粗类，避免日志里带出节点地址等细节。
func classifyError(msg string) string {
	switch {
	case msg == "":
		return "unknown"
	case strings.Contains(msg, "context deadline exceeded"), strings.Contains(msg, "Client.Timeout"):
		return "timeout"
	case strings.Contains(msg, "connection refused"):
		return "connection refused"
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "dns"):
		return "dns"
	case strings.Contains(msg, "unsupported proxy protocol"):
		return "unsupported protocol"
	case strings.Contains(msg, "invalid delay"):
		return "invalid delay"
	case strings.Contains(msg, "EOF"):
		return "eof"
	case strings.Contains(msg, "reset"):
		return "connection reset"
	default:
		return "other"
	}
}
