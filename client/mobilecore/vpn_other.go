//go:build !android

package mobilecore

func (c *Core) startVPNFromFD(data map[string]any) string {
	return failCode("unsupported_platform", "startVPNFromFD is only available on Android")
}

func (c *Core) startTun2SocksFromFD(data map[string]any) string {
	return failCode("unsupported_platform", "startTun2SocksFromFD is only available on Android")
}

func (c *Core) stopVPN(data map[string]any) string {
	return ok(map[string]any{"state": "stopped"})
}

func (c *Core) vpnStatus(data map[string]any) string {
	return ok(map[string]any{"state": "stopped"})
}
