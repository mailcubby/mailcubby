package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

// FR-045: local mode rejects cross-origin unsafe browser requests and lets
// curl/SDKs (no Origin), same-origin requests and the Vite dev proxy through.
func TestLocalModeCrossOriginProtection(t *testing.T) {
	t.Parallel()

	srv := testutil.StartServer(t, nil)
	srvWithBase := testutil.StartServer(t, func(cfg *config.Config) { cfg.BaseURL = "https://mail.dev.test" })
	client := &http.Client{Timeout: 5 * time.Second}

	tests := []struct {
		name    string
		base    string
		host    string // overrides the Host header ("" = the listener address)
		headers map[string]string
		method  string
		want    int
	}{
		{name: "curl without Origin", base: srv.HTTPAddr, want: http.StatusCreated},
		{name: "same-origin browser request", base: srv.HTTPAddr, headers: map[string]string{
			"Origin": "http://" + srv.HTTPAddr, "Sec-Fetch-Site": "same-origin",
		}, want: http.StatusCreated},
		{name: "same-origin without Sec-Fetch-Site (plain http LAN)", base: srv.HTTPAddr, headers: map[string]string{
			"Origin": "http://" + srv.HTTPAddr,
		}, want: http.StatusCreated},
		// The Vite proxy (changeOrigin: false) forwards Host: localhost:5173 and
		// the browser's Origin http://localhost:5173 and Sec-Fetch-Site: same-origin.
		{name: "Vite dev proxy", base: srv.HTTPAddr, host: "localhost:5173", headers: map[string]string{
			"Origin": "http://localhost:5173", "Sec-Fetch-Site": "same-origin",
		}, want: http.StatusCreated},
		{name: "foreign Origin", base: srv.HTTPAddr, headers: map[string]string{
			"Origin": "https://evil.example",
		}, want: http.StatusForbidden},
		{name: "Sec-Fetch-Site cross-site", base: srv.HTTPAddr, headers: map[string]string{
			"Sec-Fetch-Site": "cross-site",
		}, want: http.StatusForbidden},
		{name: "foreign Origin text/plain form post", base: srv.HTTPAddr, headers: map[string]string{
			"Origin": "https://evil.example", "Content-Type": "text/plain",
		}, want: http.StatusForbidden},
		{name: "cross-site GET is allowed (safe method)", base: srv.HTTPAddr, method: http.MethodGet, headers: map[string]string{
			"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site",
		}, want: http.StatusOK},
		{name: "reverse proxy rewriting Host, origin is BASE_URL", base: srvWithBase.HTTPAddr, host: "127.0.0.1", headers: map[string]string{
			"Origin": "https://mail.dev.test",
		}, want: http.StatusCreated},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			method := tc.method
			if method == "" {
				method = http.MethodPost
			}
			var body *bytes.Reader
			if method == http.MethodPost {
				b, err := json.Marshal(map[string]any{"name": "Cross Origin " + strconv.Itoa(i), "color": "blue"})
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				body = bytes.NewReader(b)
			} else {
				body = bytes.NewReader(nil)
			}
			req, err := http.NewRequestWithContext(t.Context(), method, "http://"+tc.base+"/api/v1/projects", body)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			if tc.host != "" {
				req.Host = tc.host
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if tc.want == http.StatusForbidden {
				var e struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&e); err != nil || e.Error.Code != "forbidden" {
					t.Fatalf("403 body code = %q (err %v), want forbidden", e.Error.Code, err)
				}
			}
		})
	}
}
