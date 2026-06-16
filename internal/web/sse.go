package web

import (
	"fmt"
	"net/http"

	"github.com/example/sshtunneld/internal/logging"
)

// sseLogs streams the in-memory log broadcaster as a Server-Sent Events feed.
// On connect we replay the ring buffer so dashboards can show recent history.
func sseLogs(bcast *logging.Broadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		ch, history := bcast.Subscribe()
		defer bcast.Unsubscribe(ch)

		for _, line := range history {
			if _, err := fmt.Fprintf(w, "data: %s\n\n", line); err != nil {
				return
			}
		}
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}
