package tunnel

import (
	"net"
	"sync"
	"sync/atomic"
)

// Stats is a per-tunnel set of atomic byte/connection counters.  All readers
// (HTTP handlers) and writers (forward goroutines) use atomic operations so
// no locks are needed in the data path.
//
// The counters are process-local: restarting sshtunneld zeros them.
type Stats struct {
	BytesIn    atomic.Uint64 // bytes read from the local side and forwarded
	BytesOut   atomic.Uint64 // bytes written to the local side
	ConnActive atomic.Int64  // currently open forwarded connections
	ConnTotal  atomic.Uint64 // cumulative count of accepted connections
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
//
// `in` selects which counter is incremented for Read calls (true → BytesIn,
// false → BytesOut).  Write always uses the opposite counter, because the
// pair (incoming, outgoing) tracks bytes from the *local* side's perspective.
type countingConn struct {
	net.Conn
	s    *Stats
	in   bool
	once sync.Once
}

// wrapConn returns a counting wrapper if s != nil, otherwise the plain conn.
// It also bumps ConnActive / ConnTotal eagerly; the caller must invoke close
// (which is wired through net.Conn.Close) for ConnActive to drain.
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
