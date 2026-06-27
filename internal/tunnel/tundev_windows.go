//go:build windows

package tunnel

import (
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wintun"
)

// wintunDevice wraps a wireguard/tun.Device into the TUNDevice interface.
// On Windows it uses the wintun.dll driver (must be beside the binary).
type wintunDevice struct {
	dev  tun.Device
	name string

	// readBuf pools a batch buffer for the single-packet Read we expose.
	readMu  sync.Mutex
	readBuf [][]byte
	sizes   []int
}

// fixedGUID ensures we always reuse the same adapter across reconnects.
var fixedGUID = windows.GUID{
	Data1: 0x82FA27BE,
	Data2: 0x0001,
	Data3: 0x4001,
	Data4: [8]byte{0xA0, 0x01, 0x5A, 0xFE, 0x11, 0x0C, 0x00, 0x01},
}

// CreateTUN creates a WinTUN adapter named "SafeLink" with a 1500-byte MTU.
// Uses a fixed GUID to prevent duplicate adapters on reconnect.
func CreateTUN() (TUNDevice, error) {
	return CreateTUNNamed("SafeLink")
}

// CreateTUNNamed creates a WinTUN adapter with the given name.
// On Windows the name is always "SafeLink" regardless of input (wintun limitation).
func CreateTUNNamed(name string) (TUNDevice, error) {
	const ifName = "SafeLink"

	// Force-close any stale adapter with the same name.
	if existing, err := wintun.OpenAdapter(ifName); err == nil {
		existing.Close()
		time.Sleep(500 * time.Millisecond)
	}

	// Retry creation a few times in case the OS is still cleaning up.
	var dev tun.Device
	var err error
	for i := 0; i < 3; i++ {
		dev, err = tun.CreateTUNWithRequestedGUID(ifName, &fixedGUID, 1500)
		if err == nil {
			break
		}
		time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
	}
	if err != nil {
		return nil, fmt.Errorf("wintun CreateTUN: %w", err)
	}

	realName, _ := dev.Name()
	if realName == "" {
		realName = ifName
	}
	batchSz := dev.BatchSize()
	return &wintunDevice{
		dev:     dev,
		name:    realName,
		readBuf: make([][]byte, batchSz),
		sizes:   make([]int, batchSz),
	}, nil
}

func (w *wintunDevice) Name() string { return w.name }

func (w *wintunDevice) Read(p []byte) (int, error) {
	w.readMu.Lock()
	defer w.readMu.Unlock()

	// Prepare batch buffers (reuse allocations where possible).
	for i := range w.readBuf {
		if cap(w.readBuf[i]) < len(p) {
			w.readBuf[i] = make([]byte, len(p))
		} else {
			w.readBuf[i] = w.readBuf[i][:len(p)]
		}
	}
	n, err := w.dev.Read(w.readBuf, w.sizes, 0)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, fmt.Errorf("tun read: no packets")
	}
	// Copy the first packet into caller's buffer.
	pktLen := w.sizes[0]
	copy(p[:pktLen], w.readBuf[0][:pktLen])
	return pktLen, nil
}

func (w *wintunDevice) Write(p []byte) (int, error) {
	bufs := [][]byte{p}
	n, err := w.dev.Write(bufs, 0)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, fmt.Errorf("tun write: 0 packets written")
	}
	return len(p), nil
}

func (w *wintunDevice) Close() error {
	return w.dev.Close()
}
