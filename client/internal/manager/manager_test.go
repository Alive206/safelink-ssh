package manager

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/example/safelink/client/internal/sshclient"
	"github.com/example/safelink/pkg/config"
)

func TestStartDoesNotLaunchSeededTunnels(t *testing.T) {
	mgr := New([]config.TunnelCfg{testTunnelConfig()}, config.ConnDefaults{}, discardLogger(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mgr.Start(ctx)

	statuses := mgr.List()
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if statuses[0].State != string(sshclient.StateStopped) {
		t.Fatalf("state = %q, want %q", statuses[0].State, sshclient.StateStopped)
	}
	if statuses[0].RunCount != 0 {
		t.Fatalf("run count = %d, want 0", statuses[0].RunCount)
	}

	mgr.mu.RLock()
	cancelFn := mgr.tunnels["dev"].cancel
	mgr.mu.RUnlock()
	if cancelFn != nil {
		t.Fatal("seeded tunnel was launched during manager start")
	}
}

func TestStartTunnelStillLaunchesManually(t *testing.T) {
	mgr := New([]config.TunnelCfg{testTunnelConfig()}, config.ConnDefaults{}, discardLogger(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr.Start(ctx)

	if err := mgr.StartTunnel("dev"); err != nil {
		t.Fatalf("StartTunnel returned error: %v", err)
	}
	defer mgr.StopAllTimeout(time.Second)

	mgr.mu.RLock()
	cancelFn := mgr.tunnels["dev"].cancel
	mgr.mu.RUnlock()
	if cancelFn == nil {
		t.Fatal("manual StartTunnel did not launch the tunnel")
	}
}

func testTunnelConfig() config.TunnelCfg {
	return config.TunnelCfg{
		Name:    "dev",
		Mode:    config.ModeLocal,
		Listen:  "127.0.0.1:0",
		Forward: "127.0.0.1:80",
		SSH: config.SSHCfg{
			Addr:     "127.0.0.1:1",
			User:     "tester",
			Password: "secret",
		},
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
