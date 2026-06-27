package tunnel

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
)

// RouteConfig describes routing setup when the TUN interface comes up.
type RouteConfig struct {
	Gateway    string   // TUN gateway IP (first IP in subnet)
	All        bool     // route 0.0.0.0/0 through TUN
	ExtraCIDRs []string // additional subnets to route
}

// ParseGateway returns the gateway IP for a client TUN subnet.
// e.g. "10.0.8.2/24" → "10.0.8.1"
func ParseGateway(subnetCIDR string) (string, error) {
	ip, ipnet, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return "", fmt.Errorf("parse subnet %q: %w", subnetCIDR, err)
	}
	network := ipnet.IP.Mask(ipnet.Mask)
	gw := make(net.IP, len(network))
	copy(gw, network)
	gw[len(gw)-1] = network[len(network)-1] | 0x01
	if gw.Equal(ip) {
		gw[len(gw)-1]++
	}
	return gw.String(), nil
}

// AddRoutes adds routing rules for the TUN interface.  Best-effort on errors.
func AddRoutes(subnet string, cfg RouteConfig) error {
	switch runtime.GOOS {
	case "windows":
		return addRoutesWindows(subnet, cfg)
	case "linux":
		return addRoutesLinux(subnet, cfg)
	case "darwin":
		return addRoutesMacOS(subnet, cfg)
	default:
		return fmt.Errorf("route add not supported on %s", runtime.GOOS)
	}
}

// DelRoutes removes routing rules for the TUN interface.  Errors are ignored.
func DelRoutes(subnet string, cfg RouteConfig) {
	switch runtime.GOOS {
	case "windows":
		delRoutesWindows(subnet, cfg)
	case "linux":
		delRoutesLinux(subnet, cfg)
	case "darwin":
		delRoutesMacOS(subnet, cfg)
	}
}

// ----- Windows -----

func addRoutesWindows(subnet string, cfg RouteConfig) error {
	gw := cfg.Gateway
	if cfg.All {
		if err := execRoute("route", "add", "0.0.0.0", "mask", "0.0.0.0", gw); err != nil {
			return err
		}
	}
	if err := execRoute("route", "add", subnet, gw); err != nil {
		return err
	}
	for _, c := range cfg.ExtraCIDRs {
		_ = execRoute("route", "add", c, gw)
	}
	return nil
}

func delRoutesWindows(subnet string, cfg RouteConfig) {
	_ = execRoute("route", "delete", "0.0.0.0", "mask", "0.0.0.0", cfg.Gateway)
	_ = execRoute("route", "delete", subnet, cfg.Gateway)
	for _, c := range cfg.ExtraCIDRs {
		_ = execRoute("route", "delete", c, cfg.Gateway)
	}
}

// ----- Linux -----

func addRoutesLinux(subnet string, cfg RouteConfig) error {
	gw := cfg.Gateway
	if cfg.All {
		if err := execRoute("ip", "route", "add", "default", "via", gw, "dev", "tun0"); err != nil {
			return err
		}
	}
	if err := execRoute("ip", "route", "add", subnet, "dev", "tun0"); err != nil {
		return err
	}
	for _, c := range cfg.ExtraCIDRs {
		_ = execRoute("ip", "route", "add", c, "via", gw, "dev", "tun0")
	}
	return nil
}

func delRoutesLinux(subnet string, cfg RouteConfig) {
	_ = execRoute("ip", "route", "del", "default", "dev", "tun0")
	_ = execRoute("ip", "route", "del", subnet, "dev", "tun0")
	for _, c := range cfg.ExtraCIDRs {
		_ = execRoute("ip", "route", "del", c, "dev", "tun0")
	}
}

// ----- macOS -----

func addRoutesMacOS(subnet string, cfg RouteConfig) error {
	if cfg.All {
		if err := execRoute("route", "add", "default", "-interface", "tun0"); err != nil {
			return err
		}
	}
	if err := execRoute("route", "add", "-net", subnet, "-interface", "tun0"); err != nil {
		return err
	}
	for _, c := range cfg.ExtraCIDRs {
		_ = execRoute("route", "add", "-net", c, "-interface", "tun0")
	}
	return nil
}

func delRoutesMacOS(subnet string, cfg RouteConfig) {
	_ = execRoute("route", "delete", "default", "-interface", "tun0")
	_ = execRoute("route", "delete", "-net", subnet, "-interface", "tun0")
	for _, c := range cfg.ExtraCIDRs {
		_ = execRoute("route", "delete", "-net", c, "-interface", "tun0")
	}
}

func execRoute(args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, string(out))
	}
	return nil
}
