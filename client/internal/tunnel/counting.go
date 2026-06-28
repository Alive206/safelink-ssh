package tunnel

import (
	"net"
	"sync"
	"sync/atomic"
)

// Stats is a per-tunnel set of atomic byte/connection counters.
type Stats struct {
	BytesIn    atomic.Uint64
	BytesOut   atomic.Uint64
	ConnActive atomic.Int64
	ConnTotal  atomic.Uint64
}

// NewStats returns a fresh, zero-valued Stats.
func NewStats() *Stats { return &Stats{} }

// Snapshot is the JSON-friendly read-only view of a Stats instance.
type Snapshot struct {
	BytesIn    uint64 `json:"bytes_in"`
	BytesOut   uint64 `json:"bytes_out"`
	ConnActive int64  `json:"conn_active"`
	ConnTotal  uint64 `json:"conn_total"`
}

// Snapshot atomically copies the current values.
func (s *Stats) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	return Snapshot{
		BytesIn:    s.BytesIn.Load(),
		BytesOut:   s.BytesOut.Load(),
		ConnActive: s.ConnActive.Load(),
		ConnTotal:  s.ConnTotal.Load(),
	}
}

// countingConn wraps net.Conn and feeds Read/Write byte counts into a Stats.
type countingConn struct {
	net.Conn
	s    *Stats
	in   bool
	once sync.Once
}

// wrapConn returns a counting wrapper if s != nil, otherwise the plain conn.
func wrapConn(c net.Conn, s *Stats, in bool) net.Conn {
	if s == nil || c == nil {
		return c
	}
	s.ConnActive.Add(1)
	s.ConnTotal.Add(1)
	return &countingConn{Conn: c, s: s, in: in}
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		if c.in {
			c.s.BytesIn.Add(uint64(n))
		} else {
			c.s.BytesOut.Add(uint64(n))
		}
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		if c.in {
			c.s.BytesOut.Add(uint64(n))
		} else {
			c.s.BytesIn.Add(uint64(n))
		}
	}
	return n, err
}

func (c *countingConn) Close() error {
	c.once.Do(func() { c.s.ConnActive.Add(-1) })
	return c.Conn.Close()
}
