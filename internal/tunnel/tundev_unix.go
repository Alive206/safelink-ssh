//go:build !windows

package tunnel

import (
	"fmt"

	"github.com/songgao/water"
)

// waterDevice wraps a songgao/water TUN interface for Linux and macOS.
type waterDevice struct {
	iface *water.Interface
}

// CreateTUN creates a kernel TUN device using the water library.
// If no name is desired, it auto-assigns one from the kernel.
func CreateTUN() (TUNDevice, error) {
	return CreateTUNNamed("")
}

// CreateTUNNamed creates (or opens) a TUN device with the specified name.
// On Linux, if the name matches a pre-created device, it reuses it.
func CreateTUNNamed(name string) (TUNDevice, error) {
	cfg := water.Config{DeviceType: water.TUN}
	if name != "" {
		cfg.Name = name
	}
	iface, err := water.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("create TUN %q: %w (run as root on Linux/macOS)", name, err)
	}
	return &waterDevice{iface: iface}, nil
}

func (w *waterDevice) Name() string                { return w.iface.Name() }
func (w *waterDevice) Read(p []byte) (int, error)  { return w.iface.Read(p) }
func (w *waterDevice) Write(p []byte) (int, error) { return w.iface.Write(p) }
func (w *waterDevice) Close() error                { return w.iface.Close() }
