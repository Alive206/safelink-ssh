// Package web provides the embedded HTTP control panel for the SafeLink server.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist/*
var distFS embed.FS

// Assets returns the frontend static files as an http.FileSystem.
func Assets() http.FileSystem {
	sub, _ := fs.Sub(distFS, "dist")
	return http.FS(sub)
}
