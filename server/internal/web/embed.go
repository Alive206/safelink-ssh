// Package web provides the embedded HTTP control panel for the SafeLink server.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist/*
var distFS embed.FS

// Assets returns the frontend static files as an http.FileSystem.
func Assets() http.FileSystem {
	sub, _ := fs.Sub(distFS, "dist")
	return http.FS(sub)
}

func spaHandler() http.Handler {
	sub, _ := fs.Sub(distFS, "dist")
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(sub, path); err != nil {
			r2 := new(http.Request)
			*r2 = *r
			u := *r.URL
			u.Path = "/"
			r2.URL = &u
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
