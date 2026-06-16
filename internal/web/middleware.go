package web

import (
	"log/slog"
	"net/http"
	"time"
)

// withLogging logs every HTTP request at info level once it completes.
func withLogging(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"latency_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

// statusWriter shadows http.ResponseWriter so we can capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

// Flush exposes http.Flusher when the underlying writer supports it.  This
// is required by the SSE handler to push events without buffering.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
