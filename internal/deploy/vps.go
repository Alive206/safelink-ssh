package deploy

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/example/sshtunneld/internal/config"
	"github.com/example/sshtunneld/internal/sshclient"
)

type DeployParams struct {
	SSH        config.SSHCfg `json:"ssh"`
	Subnet     string        `json:"subnet"`
	VPNUser    string        `json:"vpn_user"`
	VPNPass    string        `json:"vpn_pass"`
	ServerPort string        `json:"server_port"`
	Force      bool          `json:"force"`
}

type DeployResult struct {
	ServerAddr   string `json:"server_addr"`
	ServerPort   string `json:"server_port"`
	Subnet       string `json:"subnet"`
	VPNUser      string `json:"vpn_user"`
	VPNPass      string `json:"vpn_pass"`
	EgressIface  string `json:"egress_iface"`
	Status       string `json:"status"`
	BuildMethod  string `json:"build_method"`
	ServerLog    string `json:"server_log,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	TunnelName   string `json:"tunnel_name,omitempty"`
}

func DeployVPNServer(params DeployParams, log *slog.Logger) (*DeployResult, error) {
	if params.ServerPort == "" { params.ServerPort = "1562" }
	if params.Subnet == "" { params.Subnet = "10.0.8.0/24" }
	if params.SSH.Addr == "" || params.SSH.User == "" || params.VPNUser == "" || params.VPNPass == "" {
		return nil, fmt.Errorf("required: ssh.addr, ssh.user, vpn_user, vpn_pass")
	}

	client, err := sshclient.DialSSH(params.SSH, 15*time.Second)
	if err != nil { return nil, fmt.Errorf("ssh connect: %w", err) }
	defer client.Close()

	serverAddr := params.SSH.Addr
	if idx := strings.LastIndex(serverAddr, ":"); idx > 0 { serverAddr = serverAddr[:idx] }

	if !params.Force {
		if ex := checkExisting(client, serverAddr, params.ServerPort, params.VPNUser, params.VPNPass); ex != nil {
			return ex, nil
		}
	}

	run := func(cmd string) string { out, _ := sshclient.RunCommand(client, cmd); return strings.TrimSpace(out) }
	log.Info("deploy", "addr", params.SSH.Addr)

	run("mkdir -p /tmp/safelink_data")

	// Clean kill: port, screen, all safelink processes.
	sudo := fmt.Sprintf("echo '%s' | sudo -S", params.SSH.Password)
	run(sudo + " fuser -k 1562/udp 2>/dev/null; fuser -k 1562/tcp 2>/dev/null; true")
	run(sudo + " killall -9 screen safelink 2>/dev/null || true")
	run(sudo + " systemctl stop safelink 2>/dev/null || true")
	run(sudo + " screen -ls | grep safelink | cut -d. -f1 | xargs -r sudo kill 2>/dev/null || true")
	run("rm -rf /tmp/safelink_data/*")
	time.Sleep(2 * time.Second)

	// Cross-compile.
	log.Info("building")
	root := findProjectRoot()
	if root == "" { return nil, fmt.Errorf("cannot find project root") }
	tmpFile := filepath.Join(os.TempDir(), "safelink-deploy")
	defer os.Remove(tmpFile)
	b := exec.Command("go", "build", "-o", tmpFile, "-ldflags=-s -w", "./cmd/safelink")
	b.Dir = root; b.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err := b.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build: %w\n%s", err, string(out))
	}
	data, _ := os.ReadFile(tmpFile)
	if err := sshclient.UploadBytes(client, data, "/tmp/safelink_data/safelink", "755"); err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}

	// Network + TUN setup.
	iface, _ := detectEgressIface(client)
	for _, c := range []string{
		"sysctl -w net.ipv4.ip_forward=1",
		fmt.Sprintf("iptables -C INPUT -p udp --dport %s -j ACCEPT 2>/dev/null || iptables -A INPUT -p udp --dport %s -j ACCEPT", params.ServerPort, params.ServerPort),
		fmt.Sprintf("iptables -t nat -C POSTROUTING -s %s -o %s -j MASQUERADE 2>/dev/null || iptables -t nat -A POSTROUTING -s %s -o %s -j MASQUERADE", params.Subnet, iface, params.Subnet, iface),
		fmt.Sprintf("iptables -C FORWARD -i tun0 -o %s -j ACCEPT 2>/dev/null || iptables -A FORWARD -i tun0 -o %s -j ACCEPT", iface, iface),
		fmt.Sprintf("iptables -C FORWARD -i %s -o tun0 -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || iptables -A FORWARD -i %s -o tun0 -m state --state RELATED,ESTABLISHED -j ACCEPT", iface, iface),
	} { run(c) }
	for _, c := range []string{
		"modprobe tun",
		"chmod 666 /dev/net/tun",
	} { run(sudo + " " + c + " 2>/dev/null || true") }

	// Start daemon.
	run("rm -f /tmp/safelink_data/server.log")
	start := fmt.Sprintf(
		"%s setsid /tmp/safelink_data/safelink server --listen :%s --subnet %s --user %s --pass %s --nat-iface %s >/tmp/safelink_data/server.log 2>&1 &",
		sudo, params.ServerPort, params.Subnet, params.VPNUser, params.VPNPass, iface)
	run(start)
	time.Sleep(3 * time.Second)

	udp := run(fmt.Sprintf("ss -uln | grep ':%s\\b' || echo NOPE", params.ServerPort))
	status := "running"
	if strings.Contains(udp, "NOPE") { status = "not-listening" }
	log.Info("deploy done", "status", status)

	return &DeployResult{
		ServerAddr: serverAddr, ServerPort: params.ServerPort,
		Subnet: params.Subnet, VPNUser: params.VPNUser, VPNPass: params.VPNPass,
		EgressIface: iface, Status: status, BuildMethod: "cross-compile",
	}, nil
}

func checkExisting(client *ssh.Client, addr, port, user, pass string) *DeployResult {
	p, _ := sshclient.RunCommand(client, "ps aux | grep '/safelink server' | grep -v grep || echo dead")
	if strings.Contains(p, "dead") { return nil }
	sp, _ := sshclient.RunCommand(client, fmt.Sprintf("ss -uln | grep ':%s\\b' || echo nope", port))
	if strings.Contains(sp, "nope") { return nil }
	iface, _ := detectEgressIface(client)
	return &DeployResult{ServerAddr: addr, ServerPort: port, Subnet: "10.0.8.0/24", VPNUser: user, VPNPass: pass, EgressIface: iface, Status: "already-running", BuildMethod: "existing"}
}

func detectEgressIface(client *ssh.Client) (string, error) {
	out, _ := sshclient.RunCommand(client, "ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i==\"dev\") print $(i+1)}'")
	if out = strings.TrimSpace(out); out != "" { return out, nil }
	return "eth0", nil
}

func clientIP(subnet string) string {
	parts := strings.SplitN(subnet, ".", 4)
	if len(parts) != 4 { return "10.0.8.2/24" }
	lp := strings.SplitN(parts[3], "/", 2)
	pfx := "/24"
	if len(lp) > 1 { pfx = "/" + lp[1] }
	return fmt.Sprintf("%s.%s.%s.2%s", parts[0], parts[1], parts[2], pfx)
}

func CreateTunnelCfg(result *DeployResult, name string) config.TunnelCfg {
	if name == "" { name = "vpn-" + result.ServerAddr }
	ci := clientIP(result.Subnet)
	return config.TunnelCfg{Name: name, Mode: config.ModeVPN, Forward: fmt.Sprintf("%s:%s", result.ServerAddr, result.ServerPort), Listen: ci, SSH: config.SSHCfg{User: result.VPNUser, Password: result.VPNPass}, Tun: config.TunCfg{Subnet: ci, AutoRoute: true}}
}

func findProjectRoot() string {
	exe, _ := os.Executable()
	for _, d := range []string{filepath.Dir(exe), "."} {
		for i := 0; i < 5; i++ {
			if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil { return d }
			p := filepath.Dir(d)
			if p == d { break }
			d = p
		}
	}
	return ""
}
