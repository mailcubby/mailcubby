package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/api"
	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

func assertSpec123Headers(t *testing.T, h http.Header, wantHSTS bool) {
	t.Helper()

	wantCSP := "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; " +
		"frame-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"

	if got := h.Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("Content-Security-Policy = %q, want %q", got, wantCSP)
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := h.Get("Referrer-Policy"); got != "same-origin" {
		t.Errorf("Referrer-Policy = %q, want same-origin", got)
	}
	if got := h.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := h.Get("Permissions-Policy"); got != "camera=(), microphone=(), geolocation=()" {
		t.Errorf("Permissions-Policy = %q, want camera=(), microphone=(), geolocation=()", got)
	}
	if wantHSTS {
		if got := h.Get("Strict-Transport-Security"); !strings.Contains(got, "max-age=") {
			t.Errorf("Strict-Transport-Security = %q, want max-age header", got)
		}
	} else {
		if got := h.Get("Strict-Transport-Security"); got != "" {
			t.Errorf("Strict-Transport-Security = %q, want empty for non-HTTPS", got)
		}
	}
}

func TestMiddlewareSecurityHeadersAndHSTS(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		baseURL  string
		hsts     string
		wantHSTS bool
	}{
		{name: "local http no hsts", baseURL: "http://localhost:8025", hsts: "true", wantHSTS: false},
		{name: "https with hsts enabled", baseURL: "https://mail.example.test", hsts: "true", wantHSTS: true},
		{name: "https with hsts disabled", baseURL: "https://mail.example.test", hsts: "false", wantHSTS: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := testutil.NewStore(t)
			blobs, err := blob.NewStore(t.TempDir(), 3)
			if err != nil {
				t.Fatalf("blob.NewStore: %v", err)
			}
			cfg, err := config.Load(func(k string) (string, bool) {
				switch k {
				case "MAILCUBBY_BASE_URL":
					return tc.baseURL, true
				case "MAILCUBBY_HSTS":
					return tc.hsts, true
				default:
					return "", false
				}
			})
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}

			srv := api.New(cfg, st, blobs, nil, api.Options{
				WebHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("spa"))
				}),
			})
			defer srv.Close()

			for _, path := range []string{"/", "/api/v1/meta", "/api/v1/unknown", "/healthz"} {
				rec := httptest.NewRecorder()
				req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
				srv.Handler().ServeHTTP(rec, req)
				assertSpec123Headers(t, rec.Header(), tc.wantHSTS)
				if rec.Header().Get("X-Request-ID") == "" {
					t.Errorf("%s: missing X-Request-ID", path)
				}
			}
		})
	}
}

func TestMiddlewareTrustedProxiesAndLogRedaction(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	cfg, err := config.Load(func(k string) (string, bool) {
		if k == "MAILCUBBY_TRUSTED_PROXIES" {
			return "10.0.0.0/8,127.0.0.1/32", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	srv := api.New(cfg, st, blobs, nil, api.Options{Logger: logger})
	defer srv.Close()

	// Trusted proxy peer -> rightmost untrusted XFF hop is selected.
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/meta?secret=should_not_log", nil)
	req.RemoteAddr = "10.0.0.5:43210"
	req.Header.Set("X-Forwarded-For", "198.51.100.1, 203.0.113.9, 10.0.0.2")
	req.Header.Set("Authorization", "Bearer mailcubby_supersecret")
	req.Header.Set("Cookie", "mailcubby_session=secretcookie")
	req.Header.Set("X-CSRF-Token", "secretcsrf")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Health endpoints must NOT log at INFO (FR-110).
	hrec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(hrec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
	if hrec.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", hrec.Code)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, `"remote_ip":"203.0.113.9"`) {
		t.Errorf("expected resolved remote_ip 203.0.113.9 in logs, got: %s", logs)
	}
	for _, secret := range []string{"mailcubby_supersecret", "secretcookie", "secretcsrf", "should_not_log", "/healthz"} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs must not contain %q; got: %s", secret, logs)
		}
	}

	// Untrusted peer -> X-Forwarded-For must be ignored.
	logBuf.Reset()
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/meta", nil)
	req2.RemoteAddr = "198.51.100.77:12345"
	req2.Header.Set("X-Forwarded-For", "1.2.3.4")
	srv.Handler().ServeHTTP(rec2, req2)
	if !strings.Contains(logBuf.String(), `"remote_ip":"198.51.100.77"`) {
		t.Errorf("untrusted peer XFF should be ignored, got logs: %s", logBuf.String())
	}

	// RedactHeaders helper check.
	redacted := api.RedactHeaders(req.Header)
	for _, k := range []string{"Authorization", "Cookie", "X-Csrf-Token"} {
		if redacted[k] != "[REDACTED]" {
			t.Errorf("RedactHeaders[%q] = %q, want [REDACTED]", k, redacted[k])
		}
	}
}

func TestMiddlewareRecoverAndBodyLimitAndMethodNotAllowed(t *testing.T) {
	t.Parallel()

	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	srv := api.New(cfg, st, blobs, nil, api.Options{
		WebHandler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom")
		}),
	})
	defer srv.Close()

	// Panic recovery returns 500 JSON error.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/trigger-panic", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d, want 500", rec.Code)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode panic error: %v", err)
	}
	if env.Error.Code != "internal" {
		t.Fatalf("panic error code = %q, want internal", env.Error.Code)
	}
	assertSpec123Headers(t, rec.Header(), false)

	// Body > 1 MiB -> 413 payload_too_large.
	huge := `{"name":"` + strings.Repeat("a", (1<<20)+64) + `"}`
	recLarge := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recLarge, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/projects", strings.NewReader(huge)))
	if recLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize body status = %d, want 413", recLarge.Code)
	}
	if err := json.Unmarshal(recLarge.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode 413 error: %v", err)
	}
	if env.Error.Code != "payload_too_large" {
		t.Fatalf("413 error code = %q, want payload_too_large", env.Error.Code)
	}

	// Unknown JSON field -> 400 bad_request.
	recUnknown := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recUnknown, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/projects", strings.NewReader(`{"name":"ok","extra":1}`)))
	if recUnknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", recUnknown.Code)
	}

	// Wrong HTTP method on known API path -> 405 JSON error.
	rec405 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec405, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/meta", nil))
	if rec405.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/v1/meta status = %d, want 405", rec405.Code)
	}
	if rec405.Header().Get("Allow") == "" {
		t.Errorf("405 response missing Allow header")
	}
}
