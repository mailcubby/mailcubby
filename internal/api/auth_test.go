package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/api"
	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

const testBaseURL = "http://mail.example.test:8025"

type instrumentedHasher struct {
	inner       *auth.Hasher
	verifyCalls atomic.Int32
	dummyCalls  atomic.Int32
}

func newInstrumentedHasher() *instrumentedHasher {
	return &instrumentedHasher{inner: auth.NewHasher()}
}

func (h *instrumentedHasher) Hash(ctx context.Context, password string) (string, error) {
	return h.inner.Hash(ctx, password)
}

func (h *instrumentedHasher) Verify(ctx context.Context, password, encodedHash string) (bool, bool, error) {
	h.verifyCalls.Add(1)
	return h.inner.Verify(ctx, password, encodedHash)
}

func (h *instrumentedHasher) VerifyDummy(ctx context.Context, password string) error {
	h.dummyCalls.Add(1)
	return h.inner.VerifyDummy(ctx, password)
}

func (h *instrumentedHasher) resetCounts() {
	h.verifyCalls.Store(0)
	h.dummyCalls.Store(0)
}

type serverTestEnv struct {
	srv    *api.Server
	store  *store.Store
	bus    *events.Bus
	clk    *clock.Fake
	hasher *instrumentedHasher
}

func newServerModeEnv(t *testing.T) *serverTestEnv {
	t.Helper()

	dataDir := t.TempDir()
	envMap := map[string]string{
		"MAILCUBBY_MODE":                     "server",
		"MAILCUBBY_DATA_DIR":                 dataDir,
		"MAILCUBBY_BASE_URL":                 testBaseURL,
		"MAILCUBBY_APP_KEY":                  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
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
	clk := clock.NewFake(time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC))
	bus := events.NewBus()
	t.Cleanup(bus.Close)

	hasher := newInstrumentedHasher()
	srv := api.New(cfg, st, blobs, bus, api.Options{
		Version: "test",
		Clock:   clk,
		Hasher:  hasher,
	})
	t.Cleanup(srv.Close)

	return &serverTestEnv{
		srv:    srv,
		store:  st,
		bus:    bus,
		clk:    clk,
		hasher: hasher,
	}
}

func (e *serverTestEnv) seedSetupToken(t *testing.T, rawToken string) {
	t.Helper()
	b, err := json.Marshal(auth.HashToken(rawToken))
	if err != nil {
		t.Fatalf("marshal setup token hash: %v", err)
	}
	if err := e.store.SetSetting(t.Context(), "setup_token_hash", string(b), e.clk.Now().UTC()); err != nil {
		t.Fatalf("SetSetting(setup_token_hash): %v", err)
	}
}

func (e *serverTestEnv) createUser(t *testing.T, email, name, password string, isAdmin, canCreate bool, disabled bool) store.User {
	t.Helper()
	var hashPtr *string
	if password != "" {
		h, err := e.hasher.Hash(t.Context(), password)
		if err != nil {
			t.Fatalf("hash password: %v", err)
		}
		hashPtr = &h
	}
	var disabledAt *time.Time
	if disabled {
		now := e.clk.Now().UTC()
		disabledAt = &now
	}
	u, err := e.store.CreateUser(t.Context(), store.CreateUserParams{
		Email:             email,
		Name:              name,
		PasswordHash:      hashPtr,
		IsAdmin:           isAdmin,
		CanCreateProjects: canCreate,
		DisabledAt:        disabledAt,
		Now:               e.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", email, err)
	}
	return u
}

func doReq(t *testing.T, handler http.Handler, method, path string, body any, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, bodyReader)
	req.RemoteAddr = "198.51.100.10:12345"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func findSessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	return nil
}

func loginAndGetCSRF(t *testing.T, handler http.Handler, email, password string) (*http.Cookie, string) {
	t.Helper()
	rec := doReq(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"email":    email,
		"password": password,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusOK {
		t.Fatalf("login(%s) status = %d, want 200; body=%s", email, rec.Code, rec.Body.String())
	}
	cookie := findSessionCookie(rec)
	if cookie == nil || cookie.Value == "" {
		t.Fatalf("login did not set %s cookie", auth.SessionCookieName)
	}

	meRec := doReq(t, handler, http.MethodGet, "/api/v1/auth/me", nil, cookie, nil)
	if meRec.Code != http.StatusOK {
		t.Fatalf("GET /auth/me status = %d, want 200; body=%s", meRec.Code, meRec.Body.String())
	}
	var me struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(meRec.Body.Bytes(), &me); err != nil {
		t.Fatalf("unmarshal /auth/me: %v", err)
	}
	if me.CSRFToken == "" {
		t.Fatalf("GET /auth/me returned empty csrf_token in server mode")
	}
	return cookie, me.CSRFToken
}

// FR-040: Setup without token -> 403; with token -> admin created, logged in; second setup -> 409.
func TestServerSetupFlow(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	env.seedSetupToken(t, "valid-setup-token-1234567890")

	// Before setup, /meta reports setup_required=true.
	metaRec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/meta", nil, nil, nil)
	if metaRec.Code != http.StatusOK {
		t.Fatalf("GET /meta status = %d, want 200", metaRec.Code)
	}
	var meta struct {
		SetupRequired bool   `json:"setup_required"`
		InstanceName  string `json:"instance_name"`
	}
	if err := json.Unmarshal(metaRec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if !meta.SetupRequired {
		t.Fatalf("meta.setup_required = false, want true before setup")
	}

	// Setup without token -> 403.
	rec := doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/setup", map[string]any{
		"setup_token": "",
		"email":       "admin@example.test",
		"name":        "Agency Admin",
		"password":    "strong-password-123",
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("setup without token status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}

	// Setup with wrong token -> 403.
	rec = doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/setup", map[string]any{
		"setup_token": "wrong-token",
		"email":       "admin@example.test",
		"name":        "Agency Admin",
		"password":    "strong-password-123",
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("setup with wrong token status = %d, want 403", rec.Code)
	}

	// Setup with valid token -> 201, admin created & logged in.
	//nolint:gosec // G101: test fixture token
	setupToken := "valid-setup-token-1234567890"
	setupPass := "strong-password-123"
	rec = doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/setup", map[string]any{
		"setup_token":   setupToken,
		"email":         "admin@example.test",
		"name":          "Agency Admin",
		"password":      setupPass,
		"instance_name": "Acme Mailcubby",
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup with valid token status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	cookie := findSessionCookie(rec)
	if cookie == nil || cookie.Value == "" {
		t.Fatalf("setup did not set session cookie")
	}

	// Logged in immediately.
	meRec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/auth/me", nil, cookie, nil)
	if meRec.Code != http.StatusOK {
		t.Fatalf("GET /auth/me after setup status = %d, want 200", meRec.Code)
	}

	// /meta now reports setup_required=false and updated instance_name.
	metaRec = doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/meta", nil, nil, nil)
	if err := json.Unmarshal(metaRec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("unmarshal meta 2: %v", err)
	}
	if meta.SetupRequired || meta.InstanceName != "Acme Mailcubby" {
		t.Fatalf("meta after setup = %+v, want setup_required=false and instance_name=Acme Mailcubby", meta)
	}

	// Second setup -> 409.
	rec = doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/setup", map[string]any{
		"setup_token": setupToken,
		"email":       "other@example.test",
		"name":        "Other",
		"password":    setupPass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second setup status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	// Audit entry recorded.
	audits, err := env.store.ListAuditEntries(t.Context(), 10)
	if err != nil || len(audits) == 0 || audits[0].Action != "setup.complete" {
		t.Fatalf("expected setup.complete audit entry, got %+v (err=%v)", audits, err)
	}
}

// FR-043: Unknown email and user without password both run exactly one dummy argon2id verification.
func TestLoginDummyHashInstrumented(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	env.createUser(t, "nopass@example.test", "Pending Invite", "", false, false, false)
	env.createUser(t, "disabled@example.test", "Disabled User", "valid-password-1234", false, false, true)
	env.createUser(t, "active@example.test", "Active Admin", "valid-password-1234", true, true, false)

	cases := []struct {
		name       string
		email      string
		password   string
		wantStatus int
		wantVerify int32
		wantDummy  int32
	}{
		{
			name:       "unknown email uses dummy hash once",
			email:      "unknown@example.test",
			password:   "any-password-1234",
			wantStatus: http.StatusUnauthorized,
			wantVerify: 0,
			wantDummy:  1,
		},
		{
			name:       "user without password uses dummy hash once",
			email:      "nopass@example.test",
			password:   "any-password-1234",
			wantStatus: http.StatusUnauthorized,
			wantVerify: 0,
			wantDummy:  1,
		},
		{
			name:       "disabled user uses dummy hash once",
			email:      "disabled@example.test",
			password:   "valid-password-1234",
			wantStatus: http.StatusUnauthorized,
			wantVerify: 0,
			wantDummy:  1,
		},
		{
			name:       "wrong password verifies real hash once",
			email:      "active@example.test",
			password:   "wrong-password-1234",
			wantStatus: http.StatusUnauthorized,
			wantVerify: 1,
			wantDummy:  0,
		},
		{
			name:       "correct password verifies real hash once",
			email:      "active@example.test",
			password:   "valid-password-1234",
			wantStatus: http.StatusOK,
			wantVerify: 1,
			wantDummy:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env.hasher.resetCounts()
			rec := doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/auth/login", map[string]any{
				"email":    tc.email,
				"password": tc.password,
			}, nil, map[string]string{"Origin": testBaseURL})
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := env.hasher.verifyCalls.Load(); got != tc.wantVerify {
				t.Fatalf("verifyCalls = %d, want %d", got, tc.wantVerify)
			}
			if got := env.hasher.dummyCalls.Load(); got != tc.wantDummy {
				t.Fatalf("dummyCalls = %d, want %d", got, tc.wantDummy)
			}
		})
	}
}

// FR-045: POST with cookie but no CSRF header -> 403; wrong Origin -> 403.
func TestCSRFAndOriginProtection(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	env.createUser(t, "admin@example.test", "Admin", "valid-password-1234", true, true, false)
	cookie, csrfToken := loginAndGetCSRF(t, env.srv.Handler(), "admin@example.test", "valid-password-1234")

	// Missing X-CSRF-Token with valid Origin -> 403.
	rec := doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Shop",
	}, cookie, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want 403", rec.Code)
	}

	// Wrong Origin with valid X-CSRF-Token -> 403.
	rec = doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Shop",
	}, cookie, map[string]string{
		"Origin":            "http://evil.example.test",
		auth.CSRFHeaderName: csrfToken,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong Origin status = %d, want 403", rec.Code)
	}

	// Unauthenticated login with wrong Origin -> 403.
	rec = doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/auth/login", map[string]any{
		"email":    "admin@example.test",
		"password": "valid-password-1234",
	}, nil, map[string]string{"Origin": "http://evil.example.test"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("login wrong Origin status = %d, want 403", rec.Code)
	}

	// Valid Referer fallback + valid CSRF -> 201.
	rec = doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Shop",
	}, cookie, map[string]string{
		"Referer":           testBaseURL + "/projects",
		auth.CSRFHeaderName: csrfToken,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid Referer + CSRF status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

// FR-044: Idle and absolute session expiry enforced with fake clock.
func TestSessionIdleAndAbsoluteExpiry(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	env.createUser(t, "admin@example.test", "Admin", "valid-password-1234", true, true, false)

	// Idle expiry (default 7d).
	cookie, _ := loginAndGetCSRF(t, env.srv.Handler(), "admin@example.test", "valid-password-1234")
	env.clk.Advance(6 * 24 * time.Hour)
	if rec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/auth/me", nil, cookie, nil); rec.Code != http.StatusOK {
		t.Fatalf("within idle timeout status = %d, want 200", rec.Code)
	}
	// That request touched last_seen_at; now advance 7d + 1s without activity.
	env.clk.Advance(7*24*time.Hour + time.Second)
	if rec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/auth/me", nil, cookie, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("past idle timeout status = %d, want 401", rec.Code)
	}

	// Absolute expiry (default 30d) even when actively touched every 5d.
	cookie2, _ := loginAndGetCSRF(t, env.srv.Handler(), "admin@example.test", "valid-password-1234")
	for range 5 {
		env.clk.Advance(5 * 24 * time.Hour)
		if rec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/auth/me", nil, cookie2, nil); rec.Code != http.StatusOK {
			t.Fatalf("active session within 30d status = %d, want 200", rec.Code)
		}
	}
	// Total elapsed = 25d; advance 5d -> 30d absolute expiry reached.
	env.clk.Advance(5 * 24 * time.Hour)
	if rec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/auth/me", nil, cookie2, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("past absolute TTL status = %d, want 401", rec.Code)
	}
}

// SPEC 12.4: 11th failed login for same IP+email within 15 min -> 429 with Retry-After.
func TestLoginRateLimit(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	env.createUser(t, "admin@example.test", "Admin", "valid-password-1234", true, true, false)

	for i := range 10 {
		rec := doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/auth/login", map[string]any{
			"email":    "admin@example.test",
			"password": "wrong-password-1234",
		}, nil, map[string]string{"Origin": testBaseURL})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d status = %d, want 401", i+1, rec.Code)
		}
		env.clk.Advance(10 * time.Second)
	}

	// 11th attempt within 15 minutes returns 429 with Retry-After.
	rec := doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/auth/login", map[string]any{
		"email":    "admin@example.test",
		"password": "wrong-password-1234",
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th attempt status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	ra := rec.Header().Get("Retry-After")
	sec, err := strconv.Atoi(ra)
	if err != nil || sec <= 0 {
		t.Fatalf("Retry-After header = %q, want positive integer seconds", ra)
	}

	// Advance past the 15m window -> login succeeds.
	env.clk.Advance(15 * time.Minute)
	rec = doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/auth/login", map[string]any{
		"email":    "admin@example.test",
		"password": "valid-password-1234",
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusOK {
		t.Fatalf("post-window login status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// FR-044, FR-046, FR-047: Profile update, password change revoking other sessions,
// sessions list/revoke, logout, and password reset token.
func TestAccountProfilePasswordSessionsAndReset(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	user := env.createUser(t, "admin@example.test", "Admin One", "initial-password-123", true, true, false)

	cookie1, csrf1 := loginAndGetCSRF(t, env.srv.Handler(), "admin@example.test", "initial-password-123")
	cookie2, _ := loginAndGetCSRF(t, env.srv.Handler(), "admin@example.test", "initial-password-123")

	// List sessions shows 2 active sessions.
	sessRec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/auth/sessions", nil, cookie1, nil)
	if sessRec.Code != http.StatusOK {
		t.Fatalf("GET /auth/sessions status = %d, want 200", sessRec.Code)
	}
	var sessions []struct {
		ID      string `json:"id"`
		Current bool   `json:"current"`
	}
	if err := json.Unmarshal(sessRec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("unmarshal sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2", len(sessions))
	}

	// PATCH /auth/me updates name and password, and revokes cookie2.
	patchRec := doReq(t, env.srv.Handler(), http.MethodPatch, "/api/v1/auth/me", map[string]any{
		"name":             "Admin Renamed",
		"current_password": "initial-password-123",
		"new_password":     "rotated-password-456",
	}, cookie1, map[string]string{
		"Origin":            testBaseURL,
		auth.CSRFHeaderName: csrf1,
	})
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH /auth/me status = %d, want 200; body=%s", patchRec.Code, patchRec.Body.String())
	}
	if !strings.Contains(patchRec.Body.String(), "Admin Renamed") {
		t.Fatalf("PATCH /auth/me body missing updated name: %s", patchRec.Body.String())
	}

	// cookie1 still valid, cookie2 revoked (FR-044).
	if rec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/auth/me", nil, cookie1, nil); rec.Code != http.StatusOK {
		t.Fatalf("current session after password change status = %d, want 200", rec.Code)
	}
	if rec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/auth/me", nil, cookie2, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("other session after password change status = %d, want 401", rec.Code)
	}

	// Create a third session and revoke it via DELETE /auth/sessions/{id}.
	cookie3, _ := loginAndGetCSRF(t, env.srv.Handler(), "admin@example.test", "rotated-password-456")
	sessRec = doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/auth/sessions", nil, cookie1, nil)
	if err := json.Unmarshal(sessRec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("unmarshal sessions 2: %v", err)
	}
	var otherID string
	for _, s := range sessions {
		if !s.Current {
			otherID = s.ID
		}
	}
	if otherID == "" {
		t.Fatalf("expected non-current session ID")
	}
	delRec := doReq(t, env.srv.Handler(), http.MethodDelete, "/api/v1/auth/sessions/"+otherID, nil, cookie1, map[string]string{
		"Origin":            testBaseURL,
		auth.CSRFHeaderName: csrf1,
	})
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /auth/sessions/{id} status = %d, want 204", delRec.Code)
	}
	if rec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/auth/me", nil, cookie3, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session status = %d, want 401", rec.Code)
	}

	// Password reset token sets new password and revokes all sessions (FR-046).
	rawResetTok, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if _, err := env.store.CreatePasswordReset(t.Context(), store.CreatePasswordResetParams{
		UserID:    user.ID,
		TokenHash: auth.HashToken(rawResetTok),
		Now:       env.clk.Now().UTC(),
		ExpiresAt: env.clk.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreatePasswordReset: %v", err)
	}

	//nolint:gosec // G101: test fixture password
	resetPass := "brand-new-reset-pass"
	resetRec := doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/password-reset/"+rawResetTok, map[string]any{
		"password": resetPass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if resetRec.Code != http.StatusNoContent {
		t.Fatalf("POST /password-reset status = %d, want 204; body=%s", resetRec.Code, resetRec.Body.String())
	}

	// Single-use: reusing the reset token returns 410.
	resetAgain := doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/password-reset/"+rawResetTok, map[string]any{
		"password": "another-new-password",
	}, nil, map[string]string{"Origin": testBaseURL})
	if resetAgain.Code != http.StatusGone {
		t.Fatalf("reused password-reset status = %d, want 410", resetAgain.Code)
	}

	// Previous session cookie1 was revoked by reset.
	if rec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/auth/me", nil, cookie1, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session after password reset status = %d, want 401", rec.Code)
	}

	// Login with new password works, then logout revokes it.
	cookie4, csrf4 := loginAndGetCSRF(t, env.srv.Handler(), "admin@example.test", "brand-new-reset-pass")
	logoutRec := doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/auth/logout", nil, cookie4, map[string]string{
		"Origin":            testBaseURL,
		auth.CSRFHeaderName: csrf4,
	})
	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("POST /auth/logout status = %d, want 204", logoutRec.Code)
	}
	if rec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/auth/me", nil, cookie4, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session after logout status = %d, want 401", rec.Code)
	}
}

func TestServerModeAccessGatesAndLocalModeServerRoutes404(t *testing.T) {
	t.Parallel()

	// Server mode: anonymous -> 401; non-admin -> 404 on project routes, [] on list.
	env := newServerModeEnv(t)
	env.createUser(t, "admin@example.test", "Admin", "valid-password-1234", true, true, false)
	env.createUser(t, "user@example.test", "Regular User", "valid-password-1234", false, false, false)

	if _, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug:     "client-a",
		Name:     "Client A",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      env.clk.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	for _, path := range []string{"/api/v1/auth/me", "/api/v1/projects", "/api/v1/projects/client-a", "/api/v1/events"} {
		if rec := doReq(t, env.srv.Handler(), http.MethodGet, path, nil, nil, nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous GET %s status = %d, want 401", path, rec.Code)
		}
	}

	userCookie, userCSRF := loginAndGetCSRF(t, env.srv.Handler(), "user@example.test", "valid-password-1234")
	if rec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/projects/client-a", nil, userCookie, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("non-admin GET /projects/client-a status = %d, want 404", rec.Code)
	}
	if rec := doReq(t, env.srv.Handler(), http.MethodPost, "/api/v1/projects", map[string]any{"name": "Blocked"}, userCookie, map[string]string{
		"Origin":            testBaseURL,
		auth.CSRFHeaderName: userCSRF,
	}); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin POST /projects status = %d, want 403", rec.Code)
	}
	listRec := doReq(t, env.srv.Handler(), http.MethodGet, "/api/v1/projects", nil, userCookie, nil)
	if listRec.Code != http.StatusOK || strings.TrimSpace(listRec.Body.String()) != "[]" {
		t.Fatalf("non-admin GET /projects = (%d, %q), want (200, [])", listRec.Code, listRec.Body.String())
	}

	// Local mode: server-only endpoints return 404 (SPEC 5.1, 8.1).
	localCfg, err := config.Load(nil)
	if err != nil {
		t.Fatalf("local config.Load: %v", err)
	}
	localSt := testutil.NewStore(t)
	localBlobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("local blob.NewStore: %v", err)
	}
	localSrv := api.New(localCfg, localSt, localBlobs, nil, api.Options{Version: "test"})
	defer localSrv.Close()

	serverOnly := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/setup"},
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/logout"},
		{http.MethodPatch, "/api/v1/auth/me"},
		{http.MethodGet, "/api/v1/auth/sessions"},
		{http.MethodDelete, "/api/v1/auth/sessions/abc"},
		{http.MethodPost, "/api/v1/password-reset/abc"},
	}
	for _, tc := range serverOnly {
		if rec := doReq(t, localSrv.Handler(), tc.method, tc.path, map[string]any{}, nil, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("local mode %s %s status = %d, want 404", tc.method, tc.path, rec.Code)
		}
	}
}
