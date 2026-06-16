package logging

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
)

// Broadcaster fans every emitted log record (already encoded as a JSON line)
// out to a ring buffer of recent records and any number of live subscribers.
//
// Subscribers receive on a buffered channel; if a slow subscriber falls
// behind, the broadcaster drops the oldest pending message rather than
// blocking the rest of the system.
type Broadcaster struct {
	mu     sync.Mutex
	ring   [][]byte
	head   int // next index to write
	size   int // number of valid entries (<= cap(ring))
	cap    int
	subs   map[chan []byte]struct{}
	subBuf int
}

// NewBroadcaster creates a broadcaster keeping the last `ringCap` records and
// using a per-subscriber buffer of `subBuf` messages.
func NewBroadcaster(ringCap, subBuf int) *Broadcaster {
	if ringCap <= 0 {
		ringCap = 1000
	}
	if subBuf <= 0 {
		subBuf = 256
	}
	return &Broadcaster{
		ring:   make([][]byte, ringCap),
		cap:    ringCap,
		subs:   make(map[chan []byte]struct{}),
		subBuf: subBuf,
	}
}

// publish stores msg in the ring and forwards it to every live subscriber.
// msg is retained as-is; callers must not mutate it after the call.
func (b *Broadcaster) publish(msg []byte) {
	b.mu.Lock()
	b.ring[b.head] = msg
	b.head = (b.head + 1) % b.cap
	if b.size < b.cap {
		b.size++
	}
	subs := make([]chan []byte, 0, len(b.subs))
	for ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- msg:
		default:
			// Subscriber is full; drop the oldest item to make room.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- msg:
			default:
			}
		}
	}
}

// Subscribe registers a new subscriber and returns a channel + the recent
// history (oldest → newest).  Use Unsubscribe to release resources.
func (b *Broadcaster) Subscribe() (<-chan []byte, [][]byte) {
	ch := make(chan []byte, b.subBuf)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[ch] = struct{}{}
	hist := make([][]byte, 0, b.size)
	if b.size < b.cap {
		hist = append(hist, b.ring[:b.size]...)
	} else {
		hist = append(hist, b.ring[b.head:]...)
		hist = append(hist, b.ring[:b.head]...)
	}
	return ch, hist
}

// Unsubscribe removes ch from the broadcaster and closes it.  Safe to call
// multiple times.
func (b *Broadcaster) Unsubscribe(ch <-chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sc := range b.subs {
		if sc == ch {
			delete(b.subs, sc)
			close(sc)
			return
		}
	}
}

// broadcastHandler is a slog.Handler that delegates to a primary handler
// (typically the JSON stderr handler) and additionally publishes the encoded
// JSON line to a Broadcaster.  We re-encode through a sibling JSON handler so
// the stderr output and the broadcast see identical attributes.
type broadcastHandler struct {
	primary slog.Handler // writes to stderr
	mu      *sync.Mutex
	buf     *bytes.Buffer
	encoder slog.Handler // writes to buf for capture
	bcast   *Broadcaster
}

func newBroadcastHandler(primary slog.Handler, level slog.Level, b *Broadcaster) *broadcastHandler {
	buf := &bytes.Buffer{}
	enc := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level})
	return &broadcastHandler{
		primary: primary,
		mu:      &sync.Mutex{},
		buf:     buf,
		encoder: enc,
		bcast:   b,
	}
}

func (h *broadcastHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.primary.Enabled(ctx, lvl)
}

func (h *broadcastHandler) Handle(ctx context.Context, r slog.Record) error {
	if err := h.primary.Handle(ctx, r); err != nil {
		return err
	}
	// Capture the JSON-encoded form into our shared buf, then dispatch.
	h.mu.Lock()
	h.buf.Reset()
	enc := h.encoder
	if err := enc.Handle(ctx, r); err != nil {
		h.mu.Unlock()
		return err
	}
	// Strip the trailing newline that the JSON handler appends.
	line := bytes.TrimRight(h.buf.Bytes(), "\n")
	cp := make([]byte, len(line))
	copy(cp, line)
	h.mu.Unlock()

	h.bcast.publish(cp)
	return nil
}

func (h *broadcastHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &broadcastHandler{
		primary: h.primary.WithAttrs(attrs),
		mu:      h.mu,
		buf:     h.buf,
		encoder: h.encoder.WithAttrs(attrs),
		bcast:   h.bcast,
	}
}

func (h *broadcastHandler) WithGroup(name string) slog.Handler {
	return &broadcastHandler{
		primary: h.primary.WithGroup(name),
		mu:      h.mu,
		buf:     h.buf,
		encoder: h.encoder.WithGroup(name),
		bcast:   h.bcast,
	}
}
