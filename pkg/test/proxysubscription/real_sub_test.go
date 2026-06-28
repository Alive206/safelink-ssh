package proxysubscription_test

import (
	"os"
	"strings"
	"testing"

	"github.com/example/safelink/pkg/proxysubscription"
)

func TestParseRealAirportBase64Subscription(t *testing.T) {
	path := os.Getenv("SAFELINK_REAL_SUB_PATH")
	if path == "" {
		t.Skip("set SAFELINK_REAL_SUB_PATH to run with a captured subscription body")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read subscription: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	var b64 strings.Builder
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Source URL:") || strings.HasPrefix(line, "Title:") {
			continue
		}
		b64.WriteString(line)
	}

	nodes, detected, err := proxysubscription.Parse([]byte(b64.String()), proxysubscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if detected != proxysubscription.FormatURIList {
		t.Fatalf("detected = %q, want %q", detected, proxysubscription.FormatURIList)
	}
	if len(nodes) < 60 {
		t.Fatalf("len(nodes) = %d, want at least 60", len(nodes))
	}
}
