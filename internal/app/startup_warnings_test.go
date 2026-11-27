package app_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/mailcubby/mailcubby/internal/app"
	"github.com/mailcubby/mailcubby/internal/config"
)

// SPEC 5.2: server mode logs startup WARN messages when SMTP AUTH is allowed
// over plaintext or when MAILCUBBY_BASE_URL uses http://, and emits neither
// warning when TLS + https:// are configured or in local mode.
func TestAppStartupSecurityWarnings(t *testing.T) {
	t.Parallel()

	certDir := t.TempDir()
	certPath, keyPath := writeSelfSignedCert(t, certDir)

	tests := []struct {
		name             string
		env              map[string]string
		padBaseURL       bool
		wantInsecureAuth bool
		wantHTTPBaseURL  bool
	}{
		{
			name: "server mode with TLS and https logs no security warnings",
			env: map[string]string{
				"MAILCUBBY_MODE":          "server",
				"MAILCUBBY_BASE_URL":      "https://mail.example.test",
				"MAILCUBBY_APP_KEY":       testServerAppKey,
				"MAILCUBBY_SMTP_TLS_CERT": certPath,
				"MAILCUBBY_SMTP_TLS_KEY":  keyPath,
			},
			wantInsecureAuth: false,
			wantHTTPBaseURL:  false,
		},
		{
			name: "server mode with insecure SMTP auth and https warns only about insecure auth",
			env: map[string]string{
				"MAILCUBBY_MODE":                     "server",
				"MAILCUBBY_BASE_URL":                 "https://mail.example.test",
				"MAILCUBBY_APP_KEY":                  testServerAppKey,
				"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
			},
			wantInsecureAuth: true,
			wantHTTPBaseURL:  false,
		},
		{
			name: "server mode with TLS and http base URL warns only about http base URL",
			env: map[string]string{
				"MAILCUBBY_MODE":          "server",
				"MAILCUBBY_BASE_URL":      "http://mail.example.test:8025",
				"MAILCUBBY_APP_KEY":       testServerAppKey,
				"MAILCUBBY_SMTP_TLS_CERT": certPath,
				"MAILCUBBY_SMTP_TLS_KEY":  keyPath,
			},
			wantInsecureAuth: false,
			wantHTTPBaseURL:  true,
		},
		{
			name: "server mode with insecure SMTP auth and http base URL logs both warnings",
			env: map[string]string{
				"MAILCUBBY_MODE":                     "server",
				"MAILCUBBY_BASE_URL":                 "http://127.0.0.1:8025",
				"MAILCUBBY_APP_KEY":                  testServerAppKey,
				"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
			},
			wantInsecureAuth: true,
			wantHTTPBaseURL:  true,
		},
		{
			name: "server mode with TLS and untrimmed http base URL warns about http base URL",
			env: map[string]string{
				"MAILCUBBY_MODE":          "server",
				"MAILCUBBY_BASE_URL":      "http://mail.example.test:8025",
				"MAILCUBBY_APP_KEY":       testServerAppKey,
				"MAILCUBBY_SMTP_TLS_CERT": certPath,
				"MAILCUBBY_SMTP_TLS_KEY":  keyPath,
			},
			padBaseURL:       true,
			wantInsecureAuth: false,
			wantHTTPBaseURL:  true,
		},
		{
			name: "local mode with http base URL does not log server-mode warnings",
			env: map[string]string{
				"MAILCUBBY_MODE":     "local",
				"MAILCUBBY_BASE_URL": "http://localhost:8025",
			},
			wantInsecureAuth: false,
			wantHTTPBaseURL:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			cfg, err := config.Load(func(k string) (string, bool) {
				switch k {
				case "MAILCUBBY_DATA_DIR":
					return dir, true
				case "MAILCUBBY_SMTP_ADDR", "MAILCUBBY_HTTP_ADDR":
					return "127.0.0.1:0", true
				default:
					v, ok := tc.env[k]
					return v, ok
				}
			})
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			if tc.padBaseURL {
				cfg.BaseURL = "  " + cfg.BaseURL + "  "
			}

			var logBuf bytes.Buffer
			a, err := app.Open(t.Context(), cfg, app.Options{
				Logger: slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})),
			})
			if err != nil {
				t.Fatalf("app.Open: %v", err)
			}
			_ = a.Close()

			logs := logBuf.String()
			gotInsecureAuth := hasWarnLine(logs, "MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH", "plaintext")
			if gotInsecureAuth != tc.wantInsecureAuth {
				t.Errorf("insecure SMTP auth WARN present = %v, want %v\nlogs:\n%s", gotInsecureAuth, tc.wantInsecureAuth, logs)
			}

			gotHTTPBaseURL := hasWarnLine(logs, "MAILCUBBY_BASE_URL", "Secure", "HSTS")
			if gotHTTPBaseURL != tc.wantHTTPBaseURL {
				t.Errorf("http BASE_URL WARN present = %v, want %v\nlogs:\n%s", gotHTTPBaseURL, tc.wantHTTPBaseURL, logs)
			}
		})
	}
}

func hasWarnLine(logs string, substrs ...string) bool {
	for _, line := range strings.Split(logs, "\n") {
		if !strings.Contains(line, "level=WARN") {
			continue
		}
		matched := true
		for _, s := range substrs {
			if !strings.Contains(line, s) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
