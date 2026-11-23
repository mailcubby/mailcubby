package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mailcubby/mailcubby/internal/web"
)

func TestHandlerSPAAndAssets(t *testing.T) {
	t.Parallel()

	const indexHTML = `<!doctype html><html><head><script src="/theme.js"></script><script type="module" src="/assets/index-abc123.js"></script></head><body><div id="root"></div></body></html>`

	dist := fstest.MapFS{
		".gitkeep":                &fstest.MapFile{Data: nil},
		"index.html":              &fstest.MapFile{Data: []byte(indexHTML)},
		"theme.js":                &fstest.MapFile{Data: []byte("console.log('theme');")},
		"assets/index-abc123.js":  &fstest.MapFile{Data: []byte("console.log('app');")},
		"assets/index-abc123.css": &fstest.MapFile{Data: []byte("body{margin:0}")},
	}

	h := web.NewHandler(dist)

	tests := []struct {
		name             string
		method           string
		path             string
		wantStatus       int
		wantCacheControl string
		wantBodyContains string
	}{
		{
			name:             "root serves index.html",
			method:           http.MethodGet,
			path:             "/",
			wantStatus:       http.StatusOK,
			wantCacheControl: "no-cache",
			wantBodyContains: "/assets/index-abc123.js",
		},
		{
			name:             "deep link serves index.html with absolute asset URLs",
			method:           http.MethodGet,
			path:             "/p/x/m/y",
			wantStatus:       http.StatusOK,
			wantCacheControl: "no-cache",
			wantBodyContains: "/assets/",
		},
		{
			name:             "asset file served with immutable cache header",
			method:           http.MethodGet,
			path:             "/assets/index-abc123.js",
			wantStatus:       http.StatusOK,
			wantCacheControl: "public, max-age=31536000, immutable",
			wantBodyContains: "console.log('app');",
		},
		{
			name:       "missing asset returns 404",
			method:     http.MethodGet,
			path:       "/assets/missing.js",
			wantStatus: http.StatusNotFound,
		},
		{
			name:             "root static file served with no-cache",
			method:           http.MethodGet,
			path:             "/theme.js",
			wantStatus:       http.StatusOK,
			wantCacheControl: "no-cache",
			wantBodyContains: "console.log('theme');",
		},
		{
			name:       "dotfile returns 404",
			method:     http.MethodGet,
			path:       "/.gitkeep",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "api prefix returns 404",
			method:     http.MethodGet,
			path:       "/api/v1/projects",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "render prefix returns 404",
			method:     http.MethodGet,
			path:       "/render/shop/01J",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "healthz returns 404",
			method:     http.MethodGet,
			path:       "/healthz",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "readyz returns 404",
			method:     http.MethodGet,
			path:       "/readyz",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "POST rejected with 405",
			method:     http.MethodPost,
			path:       "/",
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, http.NoBody)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantCacheControl != "" {
				if got := rec.Header().Get("Cache-Control"); got != tc.wantCacheControl {
					t.Errorf("Cache-Control = %q, want %q", got, tc.wantCacheControl)
				}
			}
			if tc.wantBodyContains != "" && !strings.Contains(rec.Body.String(), tc.wantBodyContains) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), tc.wantBodyContains)
			}
		})
	}
}

func TestHandlerEmptyDist(t *testing.T) {
	t.Parallel()

	empty := fstest.MapFS{
		".gitkeep": &fstest.MapFile{Data: nil},
	}
	h := web.NewHandler(empty)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "UI not built") {
		t.Errorf("expected 'UI not built' fallback, got %q", rec.Body.String())
	}

	missingAsset := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/missing.js", http.NoBody)
	missingRec := httptest.NewRecorder()
	h.ServeHTTP(missingRec, missingAsset)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want %d", missingRec.Code, http.StatusNotFound)
	}
}

func TestEmbeddedHandler(t *testing.T) {
	t.Parallel()

	h := web.Handler()

	// Missing asset must always be 404 whether the UI is built or not.
	reqMissing := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/missing.js", http.NoBody)
	recMissing := httptest.NewRecorder()
	h.ServeHTTP(recMissing, reqMissing)
	if recMissing.Code != http.StatusNotFound {
		t.Fatalf("GET /assets/missing.js status = %d, want 404", recMissing.Code)
	}

	// Deep link serves either the built SPA (referencing /assets/) or the placeholder page.
	reqDeep := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/p/x/m/y", http.NoBody)
	recDeep := httptest.NewRecorder()
	h.ServeHTTP(recDeep, reqDeep)
	if recDeep.Code != http.StatusOK {
		t.Fatalf("GET /p/x/m/y status = %d, want 200", recDeep.Code)
	}
	body := recDeep.Body.String()
	if !strings.Contains(body, "UI not built") && !strings.Contains(body, "/assets/") {
		t.Fatalf("built deep-link HTML must reference /assets/, got: %q", body)
	}
}
