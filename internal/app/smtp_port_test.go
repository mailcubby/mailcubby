package app_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/app"
	"github.com/mailcubby/mailcubby/internal/config"
)

// FR-063: local mode announces the bound SMTP listener port; server mode and an
// explicit MAILCUBBY_SMTP_PUBLIC_PORT keep the configured value.
func TestAppAnnouncedSMTPPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		// want is the announced port; 0 means "the bound SMTP port".
		want int
	}{
		{name: "local mode announces bound port", env: map[string]string{}},
		{name: "local mode explicit public port wins", env: map[string]string{"MAILCUBBY_SMTP_PUBLIC_PORT": "2526"}, want: 2526},
		{name: "server mode keeps 587 default", env: map[string]string{
			"MAILCUBBY_MODE":                     "server",
			"MAILCUBBY_BASE_URL":                 "http://127.0.0.1:8025",
			"MAILCUBBY_APP_KEY":                  testServerAppKey,
			"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
		}, want: 587},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{
				"MAILCUBBY_DATA_DIR":  t.TempDir(),
				"MAILCUBBY_SMTP_ADDR": "127.0.0.1:0",
				"MAILCUBBY_HTTP_ADDR": "127.0.0.1:0",
			}
			for k, v := range tc.env {
				env[k] = v
			}
			cfg, err := config.Load(func(k string) (string, bool) {
				v, ok := env[k]
				return v, ok
			})
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			a, err := app.Open(ctx, cfg, app.Options{})
			if err != nil {
				cancel()
				t.Fatalf("app.Open: %v", err)
			}
			done := make(chan error, 1)
			go func() { done <- a.Serve(ctx) }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("timed out waiting for shutdown")
				}
			})

			want := tc.want
			if want == 0 {
				_, portStr, err := net.SplitHostPort(a.SMTPAddr())
				if err != nil {
					t.Fatalf("SplitHostPort: %v", err)
				}
				if want, err = strconv.Atoi(portStr); err != nil {
					t.Fatalf("Atoi: %v", err)
				}
			}
			if got := a.Config().SMTPPublicPort; got != want {
				t.Errorf("Config().SMTPPublicPort = %d, want %d", got, want)
			}

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+a.HTTPAddr()+"/api/v1/meta", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET /api/v1/meta: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			var meta struct {
				SMTP struct {
					Port int `json:"port"`
				} `json:"smtp"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
				t.Fatalf("decode meta: %v", err)
			}
			if meta.SMTP.Port != want {
				t.Errorf("/api/v1/meta smtp.port = %d, want %d", meta.SMTP.Port, want)
			}
		})
	}
}
