//go:build android

package tunnel

import "fmt"

// CreateTUN is intentionally provided by Android's VpnService layer.
func CreateTUN() (TUNDevice, error) {
	return nil, fmt.Errorf("android TUN is provided by VpnService")
}

// CreateTUNNamed is intentionally provided by Android's VpnService layer.
func CreateTUNNamed(name string) (TUNDevice, error) {
	return nil, fmt.Errorf("android TUN %q is provided by VpnService", name)
}
