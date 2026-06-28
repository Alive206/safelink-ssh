//go:build linux || darwin

// SafeLink VPN Server - Docker-deployable QUIC VPN gateway with web control panel.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/example/safelink/pkg/transport"
	"github.com/example/safelink/server/internal/vpnserver"
	"github.com/example/safelink/server/internal/web"
)

func main() {
	listen := flag.String("listen", ":1562", "QUIC listen address")
	subnet := flag.String("subnet", "10.0.8.0/24", "TUN interface subnet")
	user := flag.String("user", "admin", "VPN auth username")
	pass := flag.String("pass", "", "VPN auth password (required)")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (PEM); omit for self-signed")
	tlsKey := flag.String("tls-key", "", "TLS private key file (PEM); omit for self-signed")
	noPadding := flag.Bool("no-padding", false, "disable frame padding")
	natIface := flag.String("nat-iface", "", "enable NAT on this egress interface (Linux)")
	webAddr := flag.String("web", "0.0.0.0:8080", "Web control panel address (empty to disable)")

	flag.Parse()

	if *pass == "" {
		// Check environment variable
		*pass = os.Getenv("VPN_PASS")
	}
	if *pass == "" {
		fmt.Fprintln(os.Stderr, "error: --pass or VPN_PASS environment variable is required")
		os.Exit(1)
	}
	if envUser := os.Getenv("VPN_USER"); envUser != "" && *user == "admin" {
		*user = envUser
	}
	if envSubnet := os.Getenv("VPN_SUBNET"); envSubnet != "" && *subnet == "10.0.8.0/24" {
		*subnet = envSubnet
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// NAT setup (Linux)
	if *natIface != "" {
		if err := setupNAT(*subnet, *natIface); err != nil {
			log.Warn("NAT setup failed (try running as root)", "err", err)
		} else {
			log.Info("NAT and IP forwarding enabled", "subnet", *subnet, "iface", *natIface)
			go func() {
				<-ctx.Done()
				teardownNAT(*subnet, *natIface)
				log.Info("NAT rules cleaned up")
			}()
		}
	}

	sv := &vpnserver.VPNServer{
		ListenAddr: *listen,
		Subnet:     *subnet,
		Username:   *user,
		Password:   *pass,
		TLSOpts: transport.TLSOpts{
			CertFile: *tlsCert,
			KeyFile:  *tlsKey,
		},
		Padding: !*noPadding,
		Log:     log,
	}

	// Start web control panel.
	if *webAddr != "" {
		webSrv := web.New(*webAddr, log)
		webSrv.Start()
		defer webSrv.Shutdown()
		log.Info("web panel started", "addr", *webAddr)
	}

	log.Info("safelink-server starting", "listen", *listen, "subnet", *subnet)
	if err := sv.Run(ctx); err != nil {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
}

func setupNAT(subnet, iface string) error {
	cmds := [][]string{
		{"sysctl", "-w", "net.ipv4.ip_forward=1"},
		{"iptables", "-t", "nat", "-A", "POSTROUTING", "-s", subnet, "-o", iface, "-j", "MASQUERADE"},
		{"iptables", "-A", "FORWARD", "-i", "tun0", "-o", iface, "-j", "ACCEPT"},
		{"iptables", "-A", "FORWARD", "-i", iface, "-o", "tun0", "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w\n%s", args[0], err, string(out))
		}
	}
	return nil
}

func teardownNAT(subnet, iface string) {
	cmds := [][]string{
		{"iptables", "-t", "nat", "-D", "POSTROUTING", "-s", subnet, "-o", iface, "-j", "MASQUERADE"},
		{"iptables", "-D", "FORWARD", "-i", "tun0", "-o", iface, "-j", "ACCEPT"},
		{"iptables", "-D", "FORWARD", "-i", iface, "-o", "tun0", "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
	}
	for _, args := range cmds {
		exec.Command(args[0], args[1:]...).Run()
	}
}
