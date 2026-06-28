package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/example/safelink/pkg/config"
)

func TestLoadServerConfigReadsWebAuthUsers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	data := []byte(`
listen: :1562
subnet: 10.0.8.0/24
web:
  addr: 0.0.0.0:8080
  auth:
    users:
      - username: admin
        password_hash: "$2a$12$abc"
tls:
  cert: /cert.pem
  key: /key.pem
nat:
  enabled: true
  iface: eth0
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}

	if cfg.Web.Addr != "0.0.0.0:8080" {
		t.Fatalf("web.addr = %q", cfg.Web.Addr)
	}
	if len(cfg.Web.Auth.Users) != 1 || cfg.Web.Auth.Users[0].Username != "admin" {
		t.Fatalf("users = %#v", cfg.Web.Auth.Users)
	}
	if cfg.NAT.Iface != "eth0" || !cfg.NAT.Enabled {
		t.Fatalf("nat = %#v", cfg.NAT)
	}
}

func TestLoadServerConfigMissingFileUsesDefaults(t *testing.T) {
	cfg, err := config.LoadServerConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("LoadServerConfig missing file: %v", err)
	}

	if cfg.Listen != ":1562" {
		t.Fatalf("listen = %q", cfg.Listen)
	}
	if cfg.Web.Addr != "0.0.0.0:8080" {
		t.Fatalf("web.addr = %q", cfg.Web.Addr)
	}
	if cfg.Subnet != "10.0.8.0/24" {
		t.Fatalf("subnet = %q", cfg.Subnet)
	}
}
