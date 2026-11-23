package web

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

const (
	cacheControlImmutable = "public, max-age=31536000, immutable"
	cacheControlNoCache   = "no-cache"

	uiNotBuiltHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Mailcubby</title></head>
<body><p>UI not built: run <code>make web</code>.</p></body>
</html>
`
)

// NewHandler returns an http.Handler that serves the built SPA from dist.
// Missing files under /assets/ always return 404; non-API/render/health routes
// fall back to index.html, or a plain placeholder page if index.html is absent.
func NewHandler(dist fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		cleanPath := path.Clean("/" + r.URL.Path)
		if isReservedPrefix(cleanPath) || hasDotSegment(cleanPath) {
			http.NotFound(w, r)
			return
		}

		if strings.HasPrefix(cleanPath, "/assets/") {
			rel := strings.TrimPrefix(cleanPath, "/")
			if !serveRegularFile(w, r, dist, rel, cacheControlImmutable) {
				http.NotFound(w, r)
			}
			return
		}

		if !hasRegularFile(dist, "index.html") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", cacheControlNoCache)
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, uiNotBuiltHTML)
			}
			return
		}

		if cleanPath != "/" {
			rel := strings.TrimPrefix(cleanPath, "/")
			if serveRegularFile(w, r, dist, rel, cacheControlNoCache) {
				return
			}
		}

		if !serveRegularFile(w, r, dist, "index.html", cacheControlNoCache) {
			http.NotFound(w, r)
		}
	})
}

func isReservedPrefix(cleanPath string) bool {
	return cleanPath == "/api" || strings.HasPrefix(cleanPath, "/api/") ||
		cleanPath == "/render" || strings.HasPrefix(cleanPath, "/render/") ||
		cleanPath == "/healthz" || cleanPath == "/readyz"
}

func hasDotSegment(cleanPath string) bool {
	for _, seg := range strings.Split(cleanPath, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

func hasRegularFile(dist fs.FS, name string) bool {
	info, err := fs.Stat(dist, name)
	return err == nil && !info.IsDir()
}

func serveRegularFile(w http.ResponseWriter, r *http.Request, dist fs.FS, name, cacheControl string) bool {
	f, err := dist.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}

	rs, ok := f.(io.ReadSeeker)
	if !ok {
		data, readErr := io.ReadAll(f)
		if readErr != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return true
		}
		rs = bytes.NewReader(data)
	}

	w.Header().Set("Cache-Control", cacheControl)
	modTime := info.ModTime()
	if modTime.IsZero() {
		modTime = time.Time{}
	}
	http.ServeContent(w, r, path.Base(name), modTime, rs)
	return true
}
