// Package logging provides slog initialization tuned for sshtunneld.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// New returns a JSON slog.Logger writing to stderr with the given level.
// Unknown levels fall back to info.  stderr is used so that stdout remains
// available for any future structured RPC/control output.
func New(level string) *slog.Logger {
	logger, _ := NewWithBroadcast(level, nil)
	return logger
}

// NewWithBroadcast returns the same logger as New plus a Broadcaster that
// receives every emitted JSON-encoded record.  When bcast is nil a fresh one
// is allocated and returned so the caller can wire it into the SSE handler.
func NewWithBroadcast(level string, bcast *Broadcaster) (*slog.Logger, *Broadcaster) {
	lvl := parseLevel(level)
	primary := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	if bcast == nil {
		bcast = NewBroadcaster(1000, 256)
	}
	h := newBroadcastHandler(primary, lvl, bcast)
	return slog.New(h), bcast
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
