package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// distFS contains the prebuilt React control panel.  When the frontend has
// not been built yet (e.g. fresh checkout) the embedded directory only
// contains a placeholder index.html telling the operator how to build it.
//
//go:embed dist
var distFS embed.FS

// uiHandler returns an http.Handler that serves the embedded UI with SPA
// fallback (any path that doesn't match a real file falls back to index.html).
func uiHandler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Should never happen — would mean the embed directive failed.
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "ui not embedded", http.StatusInternalServerError)
		})
	}
	fileSrv := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip the leading "/" for fs lookups.
		name := r.URL.Path
		if name == "" || name == "/" {
			fileSrv.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(sub, name[1:]); err == nil {
			fileSrv.ServeHTTP(w, r)
			return
		}
		// SPA fallback: rewrite the URL to "/" so the FileServer returns
		// index.html.  The browser routes the original path client-side.
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileSrv.ServeHTTP(w, r2)
	})
}
