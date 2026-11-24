package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/api"
	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/smtpd"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// SPEC 12.3: verify that no passwords, session cookies, CSRF tokens, invite
// tokens, password-reset tokens, API tokens, SMTP passwords, query strings,
// app keys, or email bodies ever appear in captured Debug-level server logs or
// audit log metadata across a full server-mode workflow.
func TestIntegrationLogRedactionNoSecrets(t *testing.T) {
	t.Parallel()

	var logBuf syncBuffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	const (
		appKeyB64       = "Q1RGLVNlY3JldC1BcHAtS2V5LTMyQnl0ZXMtMTIzNDU="
		adminPass       = "SuperSecret-AdminPass-9911!"
		wrongPass       = "WrongSecret-LoginAttempt-8822!"
		invitedPass     = "InvitedSecret-UserPass-7733!"
		resetPass       = "ResetSecret-NewPass-6644!"
		changedPass     = "ChangedSecret-SelfPass-5555!"
		badSMTPValue    = "BadSecret-SMTPAttempt-4466!"
		secretQueryTerm = "UltraSecretQueryString9988"
		secretMailBody  = "Confidential-Email-Body-Payload-3377"
	)

	dataDir := t.TempDir()
	envMap := map[string]string{
		"MAILCUBBY_MODE":                     "server",
		"MAILCUBBY_DATA_DIR":                 dataDir,
		"MAILCUBBY_BASE_URL":                 testBaseURL,
		"MAILCUBBY_APP_KEY":                  appKeyB64,
		"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
		"MAILCUBBY_SMTP_AUTH_MAX_FAILURES":   "5",
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
	clk := clock.NewFake(time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC))
	bus := events.NewBus()
	t.Cleanup(bus.Close)
	ing := ingest.New(st, blobs, clk, bus, logger)
	hasher := newInstrumentedHasher()

	srv := api.New(cfg, st, blobs, bus, api.Options{
		Version:  "test",
		Clock:    clk,
		Logger:   logger,
		Ingestor: ing,
		Hasher:   hasher,
	})
	t.Cleanup(srv.Close)
	h := srv.Handler()

	smtpSrv := smtpd.New(cfg, st, ing, clk, logger)
	smtpLn := listenLoopback(t)
	go func() { _ = smtpSrv.Serve(smtpLn) }()
	t.Cleanup(func() { _ = smtpSrv.Close() })

	// Seed admin user directly (simulating headless bootstrap).
	env := &serverTestEnv{srv: srv, store: st, blobs: blobs, ing: ing, bus: bus, clk: clk, hasher: hasher}
	env.createUser(t, "admin@redact.test", "Redact Admin", adminPass, true, true, false)

	// 1. Failed login + successful login.
	_ = doReq(t, h, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"email": "admin@redact.test", "password": wrongPass,
	}, nil, map[string]string{"Origin": testBaseURL})
	adminCookie, adminCSRF := loginAndGetCSRF(t, h, "admin@redact.test", adminPass)

	// 2. Create project + SMTP credential + reveal + rotate.
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Redact Shop", "slug": "redact-shop",
	}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project = %d", rec.Code)
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/redact-shop/smtp-credentials", map[string]any{
		"label": "app",
	}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create cred = %d", rec.Code)
	}
	var credCreate struct {
		Credential struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"credential"`
		Password string `json:"password"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &credCreate)
	smtpPass1 := credCreate.Password

	credBase := "/api/v1/projects/redact-shop/smtp-credentials/" + strconv.FormatInt(credCreate.Credential.ID, 10)
	_ = doReq(t, h, http.MethodPost, credBase+"/reveal", nil, adminCookie, csrfHeaders(adminCSRF))
	rotRec := doReq(t, h, http.MethodPost, credBase+"/rotate", nil, adminCookie, csrfHeaders(adminCSRF))
	var credRotate struct {
		Password string `json:"password"`
	}
	_ = json.Unmarshal(rotRec.Body.Bytes(), &credRotate)
	smtpPass2 := credRotate.Password

	// 3. Send SMTP mail using rotated credential and secret email body, plus one bad SMTP AUTH.
	c, err := smtp.Dial(smtpLn.Addr().String())
	if err != nil {
		t.Fatalf("smtp.Dial: %v", err)
	}
	if err := c.Auth(insecurePlainAuth{user: credCreate.Credential.Username, value: smtpPass2}); err != nil {
		t.Fatalf("smtp.Auth: %v", err)
	}
	if err := c.Mail("sender@redact.test"); err != nil {
		t.Fatalf("smtp.Mail: %v", err)
	}
	if err := c.Rcpt("dest@redact.test"); err != nil {
		t.Fatalf("smtp.Rcpt: %v", err)
	}
	wc, err := c.Data()
	if err != nil {
		t.Fatalf("smtp.Data: %v", err)
	}
	_, _ = wc.Write([]byte("Subject: Confidential\r\n\r\n" + secretMailBody + "\r\n"))
	if err := wc.Close(); err != nil {
		t.Fatalf("smtp.Data Close: %v", err)
	}
	_ = c.Quit()

	// Bad SMTP auth attempt.
	badDone := make(chan error, 1)
	go func() {
		bc, derr := smtp.Dial(smtpLn.Addr().String())
		if derr != nil {
			badDone <- derr
			return
		}
		defer func() { _ = bc.Close() }()
		badDone <- bc.Auth(insecurePlainAuth{user: credCreate.Credential.Username, value: badSMTPValue})
	}()
	tk := time.NewTicker(5 * time.Millisecond)
	for {
		select {
		case authErr := <-badDone:
			tk.Stop()
			if authErr == nil {
				t.Fatalf("expected bad SMTP auth to fail")
			}
			goto doneBadSMTP
		case <-tk.C:
			clk.Advance(time.Second)
		}
	}
doneBadSMTP:

	// 4. Create project invite, preview (API + SPA path), accept with invitedPass.
	invRec := doReq(t, h, http.MethodPost, "/api/v1/projects/redact-shop/invites", map[string]any{
		"email": "bob@redact.test", "role": "member",
	}, adminCookie, csrfHeaders(adminCSRF))
	if invRec.Code != http.StatusCreated {
		t.Fatalf("create invite = %d", invRec.Code)
	}
	var invOut struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(invRec.Body.Bytes(), &invOut)
	inviteTok := invOut.URL[strings.LastIndex(invOut.URL, "/")+1:]

	_ = doReq(t, h, http.MethodGet, "/api/v1/invites/"+inviteTok, nil, nil, nil)
	_ = doReq(t, h, http.MethodGet, "/invite/"+inviteTok, nil, nil, nil)
	accRec := doReq(t, h, http.MethodPost, "/api/v1/invites/"+inviteTok+"/accept", map[string]any{
		"name": "Bob", "password": invitedPass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if accRec.Code != http.StatusOK {
		t.Fatalf("accept invite = %d; body=%s", accRec.Code, accRec.Body.String())
	}

	// 5. Generate password-reset token, hit SPA + API reset, then self-change password.
	bob, err := st.GetUserByEmail(t.Context(), "bob@redact.test")
	if err != nil {
		t.Fatalf("GetUserByEmail(bob): %v", err)
	}
	prRec := doReq(t, h, http.MethodPost, "/api/v1/admin/users/"+strconv.FormatInt(bob.ID, 10)+"/password-reset", nil, adminCookie, csrfHeaders(adminCSRF))
	var prOut struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(prRec.Body.Bytes(), &prOut)
	resetTok := prOut.URL[strings.LastIndex(prOut.URL, "/")+1:]

	_ = doReq(t, h, http.MethodGet, "/reset/"+resetTok, nil, nil, nil)
	if r := doReq(t, h, http.MethodPost, "/api/v1/password-reset/"+resetTok, map[string]any{
		"password": resetPass,
	}, nil, map[string]string{"Origin": testBaseURL}); r.Code != http.StatusNoContent {
		t.Fatalf("password reset = %d", r.Code)
	}
	bobCookie, bobCSRF := loginAndGetCSRF(t, h, "bob@redact.test", resetPass)
	if r := doReq(t, h, http.MethodPatch, "/api/v1/auth/me", map[string]any{
		"current_password": resetPass,
		"new_password":     changedPass,
	}, bobCookie, csrfHeaders(bobCSRF)); r.Code != http.StatusOK {
		t.Fatalf("password change = %d", r.Code)
	}

	// 6. Create API token and call search endpoint with a sensitive query string.
	tokRec := doReq(t, h, http.MethodPost, "/api/v1/tokens", map[string]any{
		"name": "Redact Token", "scope": "read",
	}, adminCookie, csrfHeaders(adminCSRF))
	var tokOut struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(tokRec.Body.Bytes(), &tokOut)
	apiTokenSecret := tokOut.Token
	if r := doReq(t, h, http.MethodGet, "/api/v1/projects/redact-shop/messages?query="+secretQueryTerm, nil, nil, map[string]string{
		"Authorization": "Bearer " + apiTokenSecret,
	}); r.Code != http.StatusOK {
		t.Fatalf("bearer search = %d", r.Code)
	}

	// Assert captured logs are non-empty and contain [REDACTED] for secret paths.
	logs := logBuf.String()
	if logs == "" {
		t.Fatalf("expected non-empty captured logs")
	}
	if !strings.Contains(logs, "/api/v1/invites/[REDACTED]") || !strings.Contains(logs, "/api/v1/password-reset/[REDACTED]") {
		t.Errorf("expected redacted invite/reset paths in logs, got:\n%s", logs)
	}

	// Assert ZERO secrets appear anywhere in logs.
	secrets := []struct {
		name  string
		value string
	}{
		{"appKeyB64", appKeyB64},
		{"adminPass", adminPass},
		{"wrongPass", wrongPass},
		{"invitedPass", invitedPass},
		{"resetPass", resetPass},
		{"changedPass", changedPass},
		{"adminSessionCookie", adminCookie.Value},
		{"bobSessionCookie", bobCookie.Value},
		{"adminCSRF", adminCSRF},
		{"bobCSRF", bobCSRF},
		{"inviteTok", inviteTok},
		{"resetTok", resetTok},
		{"apiTokenSecret", apiTokenSecret},
		{"smtpPass1", smtpPass1},
		{"smtpPass2", smtpPass2},
		{"badSMTPValue", badSMTPValue},
		{"secretQueryTerm", secretQueryTerm},
		{"secretMailBody", secretMailBody},
	}
	for _, sec := range secrets {
		if sec.value == "" {
			t.Fatalf("test bug: secret %s is empty", sec.name)
		}
		if strings.Contains(logs, sec.value) {
			t.Errorf("SECURITY LEAK: secret %s appeared in server logs", sec.name)
		}
	}

	// Also assert ZERO secrets appear in any audit_log entry.
	auditEntries, err := st.ListAuditEntries(t.Context(), 200)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	auditJSON, _ := json.Marshal(auditEntries)
	auditDump := string(auditJSON)
	for _, sec := range secrets {
		if strings.Contains(auditDump, sec.value) {
			t.Errorf("SECURITY LEAK: secret %s appeared in audit_log", sec.name)
		}
	}
}

// Phase 11 hardening: verify SSE closes immediately on logout, session revoke,
// and token revoke; password-reset tokens are invalidated on password change/reset;
// passwordless pending admins are excluded from last-admin protection; deleting a
// user revokes pending instance invites and refuses if the user is a sole project owner.
func TestSessionSSEAndAdminUserHardening(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	admin := env.createUser(t, "admin@hard.test", "Active Admin", pass, true, true, false)
	adminCookie, adminCSRF := loginAndGetCSRF(t, h, admin.Email, pass)

	// 1. Open SSE stream on admin session -> logout closes the stream immediately.
	t.Run("SSE closes on logout and token revoke", func(t *testing.T) {
		user := env.createUser(t, "sse@hard.test", "SSE User", pass, false, false, false)
		c1, csrf1 := loginAndGetCSRF(t, h, user.Email, pass)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/events", nil)
		req.AddCookie(c1)
		sw := newSSEStreamWriter()
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.ServeHTTP(sw, req)
		}()
		sw.waitForContains(t, "retry: 3000")

		if r := doReq(t, h, http.MethodPost, "/api/v1/auth/logout", nil, c1, csrfHeaders(csrf1)); r.Code != http.StatusNoContent {
			t.Fatalf("logout = %d", r.Code)
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("SSE stream did not close after logout")
		}

		// Token SSE stream closes immediately on token revoke.
		c2, csrf2 := loginAndGetCSRF(t, h, user.Email, pass)
		tokRec := doReq(t, h, http.MethodPost, "/api/v1/tokens", map[string]any{
			"name": "SSE Token", "scope": "read",
		}, c2, csrfHeaders(csrf2))
		var tokResp struct {
			TokenInfo struct {
				ID int64 `json:"id"`
			} `json:"token_info"`
			Token string `json:"token"`
		}
		_ = json.Unmarshal(tokRec.Body.Bytes(), &tokResp)

		tctx, tcancel := context.WithCancel(t.Context())
		defer tcancel()
		treq := httptest.NewRequestWithContext(tctx, http.MethodGet, "/api/v1/events", nil)
		treq.Header.Set("Authorization", "Bearer "+tokResp.Token)
		tsw := newSSEStreamWriter()
		tdone := make(chan struct{})
		go func() {
			defer close(tdone)
			h.ServeHTTP(tsw, treq)
		}()
		tsw.waitForContains(t, "retry: 3000")

		if r := doReq(t, h, http.MethodDelete, "/api/v1/tokens/"+strconv.FormatInt(tokResp.TokenInfo.ID, 10), nil, c2, csrfHeaders(csrf2)); r.Code != http.StatusNoContent {
			t.Fatalf("revoke token = %d", r.Code)
		}
		select {
		case <-tdone:
		case <-time.After(2 * time.Second):
			t.Fatalf("SSE stream did not close after token revoke")
		}
	})

	// 2. Outstanding password-reset tokens are invalidated when password is changed or reset.
	t.Run("password reset tokens invalidated on password change and reset", func(t *testing.T) {
		u := env.createUser(t, "resetinv@hard.test", "Reset Inv", pass, false, false, false)
		newResetToken := func() string {
			r := doReq(t, h, http.MethodPost, "/api/v1/admin/users/"+strconv.FormatInt(u.ID, 10)+"/password-reset", nil, adminCookie, csrfHeaders(adminCSRF))
			if r.Code != http.StatusOK {
				t.Fatalf("create reset = %d", r.Code)
			}
			var out struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(r.Body.Bytes(), &out)
			return out.URL[strings.LastIndex(out.URL, "/")+1:]
		}

		tok1 := newResetToken()
		tok2 := newResetToken()

		// Using tok1 invalidates tok2 as well.
		if r := doReq(t, h, http.MethodPost, "/api/v1/password-reset/"+tok1, map[string]any{
			"password": "first-reset-password-123",
		}, nil, map[string]string{"Origin": testBaseURL}); r.Code != http.StatusNoContent {
			t.Fatalf("reset with tok1 = %d", r.Code)
		}
		if r := doReq(t, h, http.MethodPost, "/api/v1/password-reset/"+tok2, map[string]any{
			"password": "second-reset-password-123",
		}, nil, map[string]string{"Origin": testBaseURL}); r.Code != http.StatusGone {
			t.Fatalf("tok2 after tok1 reset = %d, want 410", r.Code)
		}

		// Self password change invalidates a fresh reset token.
		tok3 := newResetToken()
		uCookie, uCSRF := loginAndGetCSRF(t, h, u.Email, "first-reset-password-123")
		if r := doReq(t, h, http.MethodPatch, "/api/v1/auth/me", map[string]any{
			"current_password": "first-reset-password-123",
			"new_password":     "self-changed-password-123",
		}, uCookie, csrfHeaders(uCSRF)); r.Code != http.StatusOK {
			t.Fatalf("self password change = %d", r.Code)
		}
		if r := doReq(t, h, http.MethodPost, "/api/v1/password-reset/"+tok3, map[string]any{
			"password": "stale-reset-password-123",
		}, nil, map[string]string{"Origin": testBaseURL}); r.Code != http.StatusGone {
			t.Fatalf("tok3 after self password change = %d, want 410", r.Code)
		}
	})

	// 3. Passwordless pending admin is excluded from last-admin protection,
	//    and deleting a pending user revokes their pending instance invite.
	t.Run("passwordless pending admin does not unlock last-admin protection and delete revokes instance invite", func(t *testing.T) {
		createRec := doReq(t, h, http.MethodPost, "/api/v1/admin/users", map[string]any{
			"email":    "pending-admin@hard.test",
			"name":     "Pending Admin",
			"is_admin": true,
		}, adminCookie, csrfHeaders(adminCSRF))
		if createRec.Code != http.StatusCreated {
			t.Fatalf("create pending admin = %d", createRec.Code)
		}
		var out struct {
			User struct {
				ID int64 `json:"id"`
			} `json:"user"`
			InviteURL string `json:"invite_url"`
		}
		_ = json.Unmarshal(createRec.Body.Bytes(), &out)
		pendingInviteTok := out.InviteURL[strings.LastIndex(out.InviteURL, "/")+1:]

		// Active admin STILL cannot demote/disable/delete themselves because pending admin has no password yet.
		if r := doReq(t, h, http.MethodPatch, "/api/v1/admin/users/"+strconv.FormatInt(admin.ID, 10), map[string]any{
			"is_admin": false,
		}, adminCookie, csrfHeaders(adminCSRF)); r.Code != http.StatusConflict {
			t.Fatalf("demoting only active admin with passwordless pending admin = %d, want 409", r.Code)
		}
		if r := doReq(t, h, http.MethodDelete, "/api/v1/admin/users/"+strconv.FormatInt(admin.ID, 10), nil, adminCookie, csrfHeaders(adminCSRF)); r.Code != http.StatusConflict {
			t.Fatalf("deleting only active admin with passwordless pending admin = %d, want 409", r.Code)
		}

		// Deleting the pending admin succeeds and revokes their pending instance invite.
		if r := doReq(t, h, http.MethodDelete, "/api/v1/admin/users/"+strconv.FormatInt(out.User.ID, 10), nil, adminCookie, csrfHeaders(adminCSRF)); r.Code != http.StatusNoContent {
			t.Fatalf("delete pending admin = %d, want 204", r.Code)
		}
		if r := doReq(t, h, http.MethodGet, "/api/v1/invites/"+pendingInviteTok, nil, nil, nil); r.Code != http.StatusGone {
			t.Fatalf("deleted user's instance invite preview = %d, want 410", r.Code)
		}
	})

	// 4. Admin cannot delete a user who is the sole owner of a project (409).
	t.Run("cannot delete sole owner of a project", func(t *testing.T) {
		soleOwner := env.createUser(t, "sole@hard.test", "Sole Owner", pass, false, true, false)
		proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
			Slug: "sole-owned", Name: "Sole Owned", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		if _, err := env.store.AddProjectMember(t.Context(), proj.ID, soleOwner.ID, "owner", env.clk.Now().UTC()); err != nil {
			t.Fatalf("AddProjectMember: %v", err)
		}

		if r := doReq(t, h, http.MethodDelete, "/api/v1/admin/users/"+strconv.FormatInt(soleOwner.ID, 10), nil, adminCookie, csrfHeaders(adminCSRF)); r.Code != http.StatusConflict {
			t.Fatalf("delete sole project owner = %d, want 409; body=%s", r.Code, r.Body.String())
		}

		// Once a second owner is added, deletion succeeds.
		secondOwner := env.createUser(t, "second@hard.test", "Second Owner", pass, false, false, false)
		if _, err := env.store.AddProjectMember(t.Context(), proj.ID, secondOwner.ID, "owner", env.clk.Now().UTC()); err != nil {
			t.Fatalf("AddProjectMember(second): %v", err)
		}
		if r := doReq(t, h, http.MethodDelete, "/api/v1/admin/users/"+strconv.FormatInt(soleOwner.ID, 10), nil, adminCookie, csrfHeaders(adminCSRF)); r.Code != http.StatusNoContent {
			t.Fatalf("delete co-owner = %d, want 204; body=%s", r.Code, r.Body.String())
		}
	})
}
