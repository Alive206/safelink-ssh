// sshtunneld is a YAML-driven SSH tunnel daemon supporting -L / -R / -D
// forwarding modes simultaneously, with auto-reconnect, keepalive, and an
// embedded HTTP/UI control panel.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/example/sshtunneld/internal/daemon"
	"github.com/example/sshtunneld/internal/transport"
	"github.com/example/sshtunneld/internal/tunnel"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "passwd":
			os.Exit(runPasswd(os.Args[2:]))
		case "server":
			os.Exit(runServer(os.Args[2:]))
		case "-h", "--help", "help":
			printUsage()
			return
		}
	}

	cfgPath := flag.String("config", "configs/sshtunneld.yaml", "path to YAML config file")
	noInit := flag.Bool("no-init", false, "do not auto-create the config file on first run")
	noOpen := flag.Bool("no-open", false, "do not open the browser at startup")
	flag.Parse()

	if err := daemon.RunWithOptions(*cfgPath, daemon.Options{
		AutoInit:    !*noInit,
		OpenBrowser: !*noOpen,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "sshtunneld:", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage:
  sshtunneld [-config PATH] [-no-init] [-no-open]
                                 run the daemon (default subcommand)
                                 first run auto-creates configs/sshtunneld.yaml
                                 with a random admin password and opens the UI
  sshtunneld passwd [USER]       prompt for a password and print
                                 a YAML user entry with a bcrypt hash
  sshtunneld server [flags]      start a QUIC-based VPN server
      --listen :1562             QUIC listen address
      --subnet 10.0.8.1/24       TUN interface subnet
      --user admin               auth username
      --pass secret              auth password`)
}

// runPasswd is a tiny helper to generate the bcrypt hash that goes into
// web.auth.users[].password_hash.
func runPasswd(args []string) int {
	user := ""
	if len(args) > 0 {
		user = args[0]
	}
	if user == "" {
		fmt.Fprint(os.Stderr, "Username: ")
		r := bufio.NewReader(os.Stdin)
		line, err := r.ReadString('\n')
		if err != nil {
			fmt.Fprintln(os.Stderr, "read username:", err)
			return 1
		}
		user = strings.TrimSpace(line)
	}
	if user == "" {
		fmt.Fprintln(os.Stderr, "username is required")
		return 1
	}

	var pw []byte
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Password: ")
		var err error
		pw, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read password:", err)
			return 1
		}
	} else {
		r := bufio.NewReader(os.Stdin)
		line, err := r.ReadString('\n')
		if err != nil && len(line) == 0 {
			fmt.Fprintln(os.Stderr, "read password:", err)
			return 1
		}
		pw = []byte(strings.TrimRight(line, "\r\n"))
	}
	if len(pw) == 0 {
		fmt.Fprintln(os.Stderr, "password is required")
		return 1
	}

	hash, err := bcrypt.GenerateFromPassword(pw, 12)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bcrypt:", err)
		return 1
	}

	fmt.Println("# Paste under web.auth.users in your YAML:")
	fmt.Printf("- username: %s\n", user)
	fmt.Printf("  password_hash: %q\n", string(hash))
	return 0
}

// runServer starts the QUIC-based VPN server subcommand.
func runServer(args []string) int {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	listen := fs.String("listen", ":1562", "QUIC listen address")
	subnet := fs.String("subnet", "10.0.8.1/24", "TUN interface subnet")
	user := fs.String("user", "admin", "auth username")
	pass := fs.String("pass", "", "auth password")
	tlsCert := fs.String("tls-cert", "", "TLS certificate file (PEM); omit for self-signed")
	tlsKey := fs.String("tls-key", "", "TLS private key file (PEM); omit for self-signed")
	noPadding := fs.Bool("no-padding", false, "disable frame padding (not recommended)")
	natIface := fs.String("nat-iface", "", "enable NAT + IP forwarding (Linux, specify egress interface, e.g. eth0)")
	_ = fs.Parse(args)

	if *pass == "" {
		fmt.Fprintln(os.Stderr, "server: --pass is required")
		return 1
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Setup NAT and IP forwarding on Linux if requested.
	if *natIface != "" {
		if err := setupNAT(*subnet, *natIface); err != nil {
			log.Warn("NAT setup failed (try running as root)", "err", err)
		} else {
			log.Info("NAT and IP forwarding enabled", "subnet", *subnet, "iface", *natIface)
			// Schedule cleanup on shutdown.
			go func() {
				<-ctx.Done()
				teardownNAT(*subnet, *natIface)
				log.Info("NAT rules cleaned up")
			}()
		}
	}

	sv := &tunnel.VPNServer{
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

	log.Info("vpn server starting", "listen", *listen, "subnet", *subnet)
	if err := sv.Run(ctx); err != nil {
		log.Error("vpn server error", "err", err)
		return 1
	}
	return 0
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
