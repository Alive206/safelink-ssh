package main

import (
	"fmt"
	"os"
	"time"

	"github.com/example/sshtunneld/internal/config"
	"github.com/example/sshtunneld/internal/sshclient"
)

func main() {
	cfg := config.SSHCfg{
		Addr:     "159.75.35.104:22",
		User:     "ubuntu",
		Password: "lyhappy2018.",
	}

	client, err := sshclient.DialSSH(cfg, 15*time.Second)
	if err != nil {
		fmt.Println("SSH connect failed:", err)
		return
	}
	defer client.Close()

	sudo := "echo 'lyhappy2018.' | sudo -S"
	run := func(cmd string) string {
		out, _ := sshclient.RunCommand(client, cmd)
		return out
	}

	// 1. Kill old server
	fmt.Println("[1] Killing old server...")
	run(sudo + " killall -9 safelink 2>/dev/null; sleep 1")
	run(sudo + " ip link del tun0 2>/dev/null")

	// 2. Upload new binary
	fmt.Println("[2] Uploading new binary...")
	tmpPath := os.TempDir() + string(os.PathSeparator) + "safelink-deploy"
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		fmt.Println("Read binary failed:", err)
		return
	}
	fmt.Printf("    Binary size: %d bytes\n", len(data))
	if err := sshclient.UploadBytes(client, data, "/tmp/safelink_data/safelink", "755"); err != nil {
		fmt.Println("Upload failed:", err)
		return
	}
	fmt.Println("    Upload done")

	// 3. Start server
	fmt.Println("[3] Starting server...")
	start := sudo + " bash -c 'nohup /tmp/safelink_data/safelink server --listen :1562 --subnet 10.0.8.1/24 --user vpn --pass vpn123 --nat-iface eth0 > /tmp/safelink_data/server.log 2>&1 & echo $!'"
	out := run(start)
	fmt.Println("    PID:", out)

	time.Sleep(3 * time.Second)

	// 4. Verify
	fmt.Println("[4] Verifying...")
	out = run("cat /tmp/safelink_data/server.log")
	fmt.Println("=== Server Log ===")
	fmt.Println(out)

	out = run(sudo + " ss -ulnp | grep 1562")
	fmt.Println("=== UDP Listen ===")
	fmt.Println(out)

	fmt.Println("\nDone! Server should be ready for client connection.")
}
