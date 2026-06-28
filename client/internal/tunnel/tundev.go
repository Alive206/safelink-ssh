package tunnel

// TUNDevice is the platform-independent interface for a TUN virtual network
// interface.
type TUNDevice interface {
	// Read reads one IP packet into p and returns the number of bytes read.
	Read(p []byte) (int, error)
	// Write writes one IP packet from p.
	Write(p []byte) (int, error)
	// Name returns the OS-level interface name (e.g. "tun0" or "SafeLink").
	Name() string
	// Close tears down the TUN device.
	Close() error
}
