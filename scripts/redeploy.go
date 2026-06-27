// +build ignore

package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/example/sshtunneld/internal/config"
	"github.com/example/sshtunneld/internal/deploy"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	params := deploy.DeployParams{
		SSH: config.SSHCfg{
			Addr:     "159.75.35.104:22",
			User:     "ubuntu",
			Password: "lyhappy2018.",
		},
		Subnet:     "10.0.8.0/24",
		VPNUser:    "vpn",
		VPNPass:    "vpn3456",
		ServerPort: "1562",
		Force:      true,
	}

	fmt.Println("Starting VPN server deployment...")
	result, err := deploy.DeployVPNServer(params, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Deploy failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n=== Deploy Result ===\n")
	fmt.Printf("Server: %s:%s\n", result.ServerAddr, result.ServerPort)
	fmt.Printf("Status: %s\n", result.Status)
	fmt.Printf("Build:  %s\n", result.BuildMethod)
	if result.ErrorMessage != "" {
		fmt.Printf("Error:  %s\n", result.ErrorMessage)
	}
	if result.ServerLog != "" {
		fmt.Printf("Log:    %s\n", result.ServerLog)
	}
}
