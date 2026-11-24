package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/api"
	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

// SPEC 12.4: API rate limit (MAILCUBBY_API_RATE_LIMIT) per session and per API token
// in server mode, and exempt in local mode.
func TestAPIRateLimitPerSessionAndToken(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	envMap := map[string]string{
		"MAILCUBBY_MODE":                     "server",
		"MAILCUBBY_DATA_DIR":                 dataDir,
		"MAILCUBBY_BASE_URL":                 testBaseURL,
		"MAILCUBBY_APP_KEY":                  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
		"MAILCUBBY_API_RATE_LIMIT":           "3/1m",
	}
	cfg, err := config.Load(func(k string) (string, bool) {
		v, ok := envMap[k]
		return v, ok
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	st := testutil.NewStore(t)
	blobs, err := blob.NewStore(dataDir, cfg.ZstdLevel)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
	bus := events.NewBus()
	t.Cleanup(bus.Close)
	hasher := newInstrumentedHasher()

	srv := api.New(cfg, st, blobs, bus, api.Options{
		Version: "test",
		Clock:   clk,
		Hasher:  hasher,
	})
	t.Cleanup(srv.Close)
	h := srv.Handler()

	env := &serverTestEnv{srv: srv, store: st, blobs: blobs, bus: bus, clk: clk, hasher: hasher}
	const pass = "correct-horse-battery-staple"
	user := env.createUser(t, "rl@example.test", "RL User", pass, true, true, false)

	// Session 1: login uses an unauthenticated endpoint, then /auth/me (1st token),
	// POST /tokens (2nd token), GET /projects (3rd token) -> 4th call returns 429.
	cookie1, csrf1 := loginAndGetCSRF(t, h, user.Email, pass) // consumes 1 token (/auth/me)

	tokRec := doReq(t, h, http.MethodPost, "/api/v1/tokens", map[string]any{
		"name":  "Rate Limit Token",
		"scope": "read",
	}, cookie1, csrfHeaders(csrf1)) // consumes 2nd token
	if tokRec.Code != http.StatusCreated {
		t.Fatalf("POST /tokens status = %d, want 201; body=%s", tokRec.Code, tokRec.Body.String())
	}
	var tokResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(tokRec.Body.Bytes(), &tokResp); err != nil {
		t.Fatalf("unmarshal token: %v", err)
	}

	rec := doReq(t, h, http.MethodGet, "/api/v1/projects", nil, cookie1, nil) // consumes 3rd token
	if rec.Code != http.StatusOK {
		t.Fatalf("3rd session request status = %d, want 200", rec.Code)
	}

	// 4th request on session 1 is rate-limited with 429 + Retry-After.
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects", nil, cookie1, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th session request status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" {
		t.Fatalf("missing Retry-After header on 429")
	}

	// API token has its own independent bucket of 3/1m.
	bearerHdr := map[string]string{"Authorization": "Bearer " + tokResp.Token}
	for i := range 3 {
		rec = doReq(t, h, http.MethodGet, "/api/v1/projects", nil, nil, bearerHdr)
		if rec.Code != http.StatusOK {
			t.Fatalf("token request %d status = %d, want 200", i+1, rec.Code)
		}
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects", nil, nil, bearerHdr)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th token request status = %d, want 429", rec.Code)
	}
	if sec, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || sec < 1 {
		t.Fatalf("invalid Retry-After = %q", rec.Header().Get("Retry-After"))
	}

	// Advance fake clock 20s (3/60s = 1 token every 20s) -> 1 token refilled.
	clk.Advance(20 * time.Second)
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects", nil, cookie1, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("post-refill session request status = %d, want 200", rec.Code)
	}

	// Local mode is NOT rate limited even with a low APIRateLimit value.
	localCfg := config.Config{
		Mode:                     config.ModeLocal,
		DefaultProject:           "default",
		APIRateLimit:             config.RateLimit{Count: 1, Window: time.Minute},
		APIWaitMaxTimeout:        30 * time.Second,
		SSEMaxConnectionsPerUser: 10,
	}
	localSrv := api.New(localCfg, st, blobs, bus, api.Options{Clock: clk})
	t.Cleanup(localSrv.Close)
	for i := range 10 {
		lrec := doReq(t, localSrv.Handler(), http.MethodGet, "/api/v1/projects", nil, nil, nil)
		if lrec.Code != http.StatusOK {
			t.Fatalf("local mode request %d status = %d, want 200 (local mode must not be rate limited)", i+1, lrec.Code)
		}
	}
}

// SPEC 12.4: Invite preview/accept and password reset endpoints enforce 20 requests
// per 15 minutes per IP with 429 + Retry-After, resetting after the window.
func TestInviteAndPasswordResetRateLimits(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()

	doIPReq := func(method, targetPath, remoteAddr string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), method, targetPath, strings.NewReader(body))
		req.RemoteAddr = remoteAddr
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", testBaseURL)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	t.Run("invite preview and accept share 20/15m per IP", func(t *testing.T) {
		const ipA = "203.0.113.10:12345"
		const ipB = "203.0.113.11:12345"

		for i := range 15 {
			rec := doIPReq(http.MethodGet, "/api/v1/invites/unknown-token", ipA, "")
			if rec.Code != http.StatusGone {
				t.Fatalf("preview %d status = %d, want 410", i+1, rec.Code)
			}
		}
		for i := range 5 {
			rec := doIPReq(http.MethodPost, "/api/v1/invites/unknown-token/accept", ipA, `{}`)
			if rec.Code != http.StatusGone {
				t.Fatalf("accept %d status = %d, want 410", i+1, rec.Code)
			}
		}

		// 21st request from ipA returns 429 with Retry-After.
		rec := doIPReq(http.MethodGet, "/api/v1/invites/unknown-token", ipA, "")
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("21st invite request status = %d, want 429; body=%s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Fatalf("missing Retry-After on invite 429")
		}

		// Different IP is unaffected.
		recB := doIPReq(http.MethodGet, "/api/v1/invites/unknown-token", ipB, "")
		if recB.Code != http.StatusGone {
			t.Fatalf("other IP status = %d, want 410", recB.Code)
		}

		// After 15m window, ipA is allowed again.
		env.clk.Advance(15*time.Minute + time.Second)
		recAfter := doIPReq(http.MethodGet, "/api/v1/invites/unknown-token", ipA, "")
		if recAfter.Code != http.StatusGone {
			t.Fatalf("post-window invite status = %d, want 410", recAfter.Code)
		}
	})

	t.Run("password reset enforces 20/15m per IP", func(t *testing.T) {
		const ipA = "203.0.113.20:12345"
		const ipB = "203.0.113.21:12345"
		const body = `{"password":"a-valid-long-password-123"}`

		for i := range 20 {
			rec := doIPReq(http.MethodPost, "/api/v1/password-reset/unknown-token", ipA, body)
			if rec.Code != http.StatusGone {
				t.Fatalf("reset %d status = %d, want 410", i+1, rec.Code)
			}
		}

		// 21st request from ipA returns 429 with Retry-After.
		rec := doIPReq(http.MethodPost, "/api/v1/password-reset/unknown-token", ipA, body)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("21st reset request status = %d, want 429; body=%s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Fatalf("missing Retry-After on password-reset 429")
		}

		// Different IP is unaffected.
		recB := doIPReq(http.MethodPost, "/api/v1/password-reset/unknown-token", ipB, body)
		if recB.Code != http.StatusGone {
			t.Fatalf("other IP reset status = %d, want 410", recB.Code)
		}

		// After 15m window, ipA is allowed again.
		env.clk.Advance(15*time.Minute + time.Second)
		recAfter := doIPReq(http.MethodPost, "/api/v1/password-reset/unknown-token", ipA, body)
		if recAfter.Code != http.StatusGone {
			t.Fatalf("post-window reset status = %d, want 410", recAfter.Code)
		}
	})
}
