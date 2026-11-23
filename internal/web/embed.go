// Package web serves the embedded single-page React application with SPA
// fallback and cache headers.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var embeddedDist embed.FS

// Handler returns an http.Handler serving the embedded Vite build from dist/.
func Handler() http.Handler {
	sub, err := fs.Sub(embeddedDist, "dist")
	if err != nil {
		// "dist" is always present in embeddedDist because dist/.gitkeep is committed.
		panic("web: sub embedded dist: " + err.Error())
	}
	return NewHandler(sub)
}
