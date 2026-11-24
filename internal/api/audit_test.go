package api_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/smtpd"
	"github.com/mailcubby/mailcubby/internal/store"
)

// allFR105Actions lists all 31 audited actions mandated by SPEC FR-105.
var allFR105Actions = []string{
	"setup.complete",
	"auth.login",
	"auth.login_failed",
	"auth.logout",
	"user.create",
	"user.update",
	"user.disable",
	"user.enable",
	"user.delete",
	"user.password_reset_link",
	"user.password_change",
	"project.create",
	"project.update",
	"project.delete",
	"project.admin_access",
	"member.add",
	"member.update",
	"member.remove",
	"invite.create",
	"invite.revoke",
	"invite.accept",
	"smtp_credential.create",
	"smtp_credential.reveal",
	"smtp_credential.rotate",
	"smtp_credential.update",
	"smtp_credential.delete",
	"smtp.auth_lockout",
	"api_token.create",
	"api_token.revoke",
	"messages.delete_all",
	"settings.update",
}

type auditEntryDTO struct {
	ID          int64          `json:"id"`
	At          string         `json:"at"`
	ActorType   string         `json:"actor_type"`
	ActorUserID *int64         `json:"actor_user_id"`
	ActorLabel  *string        `json:"actor_label"`
	ProjectID   *int64         `json:"project_id"`
	ProjectSlug *string        `json:"project_slug"`
	Action      string         `json:"action"`
	TargetType  *string        `json:"target_type"`
	TargetID    *string        `json:"target_id"`
	IP          *string        `json:"ip"`
	Metadata    map[string]any `json:"metadata"`
}

type auditListDTO struct {
	Items      []auditEntryDTO `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

// FR-105: exercise every single one of the 31 FR-105 actions through the real
// HTTP and SMTP boundaries and assert each produces an audit entry visible via
// the project and admin audit APIs with pagination and filtering.
func TestAuditLogAllFR105ActionsAndEndpoints(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"
	const pass2 = "second-strong-password-here"

	// 1. setup.complete
	setupVal := "audit-setup-1234567890"
	env.seedSetupToken(t, setupVal)
	rec := doReq(t, h, http.MethodPost, "/api/v1/setup", map[string]any{
		"setup_token":   setupVal,
		"email":         "admin@audit.test",
		"name":          "Audit Admin",
		"password":      pass,
		"instance_name": "Audit Instance",
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /setup status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	// 2. auth.login_failed & 3. auth.login
	rec = doReq(t, h, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"email":    "admin@audit.test",
		"password": "wrong-password-123",
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad login status = %d, want 401", rec.Code)
	}
	adminCookie, adminCSRF := loginAndGetCSRF(t, h, "admin@audit.test", pass)

	// 4. user.create (admin creates user A + user B)
	createUser := func(email, name string, canCreate bool) (int64, string) {
		t.Helper()
		r := doReq(t, h, http.MethodPost, "/api/v1/admin/users", map[string]any{
			"email":               email,
			"name":                name,
			"can_create_projects": canCreate,
		}, adminCookie, csrfHeaders(adminCSRF))
		if r.Code != http.StatusCreated {
			t.Fatalf("admin create user %s = %d; body=%s", email, r.Code, r.Body.String())
		}
		var out struct {
			User struct {
				ID int64 `json:"id"`
			} `json:"user"`
			InviteURL string `json:"invite_url"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal create user: %v", err)
		}
		tok := out.InviteURL[strings.LastIndex(out.InviteURL, "/")+1:]
		return out.User.ID, tok
	}
	userAID, userAInviteTok := createUser("owner@audit.test", "Owner A", true)
	userBID, _ := createUser("temp@audit.test", "Temp User", false)

	// 5. invite.accept (instance invite sets password for user A)
	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+userAInviteTok+"/accept", map[string]any{
		"password": pass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept instance invite = %d; body=%s", rec.Code, rec.Body.String())
	}

	// 6. user.update, 7. user.disable, 8. user.enable, 9. user.password_reset_link, 10. user.delete
	rec = doReq(t, h, http.MethodPatch, "/api/v1/admin/users/"+strconv.FormatInt(userBID, 10), map[string]any{
		"name": "Temp Renamed",
	}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("user.update = %d", rec.Code)
	}
	rec = doReq(t, h, http.MethodPatch, "/api/v1/admin/users/"+strconv.FormatInt(userBID, 10), map[string]any{
		"disabled": true,
	}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("user.disable = %d", rec.Code)
	}
	rec = doReq(t, h, http.MethodPatch, "/api/v1/admin/users/"+strconv.FormatInt(userBID, 10), map[string]any{
		"disabled": false,
	}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("user.enable = %d", rec.Code)
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/admin/users/"+strconv.FormatInt(userAID, 10)+"/password-reset", nil, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("user.password_reset_link = %d", rec.Code)
	}
	var resetResp struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resetResp)
	resetTok := resetResp.URL[strings.LastIndex(resetResp.URL, "/")+1:]

	// 11. user.password_change (via password reset)
	rec = doReq(t, h, http.MethodPost, "/api/v1/password-reset/"+resetTok, map[string]any{
		"password": pass2,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("password reset = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Delete user B
	rec = doReq(t, h, http.MethodDelete, "/api/v1/admin/users/"+strconv.FormatInt(userBID, 10), nil, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("user.delete = %d", rec.Code)
	}

	// Log in as Owner A
	ownerCookie, ownerCSRF := loginAndGetCSRF(t, h, "owner@audit.test", pass2)

	// 12. project.create & 13. project.update
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Audit Shop", "slug": "audit-shop", "color": "blue",
	}, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("project.create = %d", rec.Code)
	}
	rec = doReq(t, h, http.MethodPatch, "/api/v1/projects/audit-shop", map[string]any{
		"name": "Audit Shop Updated",
	}, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("project.update = %d", rec.Code)
	}

	// 14. project.admin_access (admin accesses project they are not an explicit member of)
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/audit-shop", nil, adminCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin access project = %d", rec.Code)
	}

	// 15. invite.create, 16. invite.revoke, 17. member.add (via invite accept), 18. member.update, 19. member.remove
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/audit-shop/invites", map[string]any{
		"email": "revoked@audit.test", "role": "viewer",
	}, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite.create(revoke) = %d", rec.Code)
	}
	var invRevoke struct {
		Invite struct {
			ID int64 `json:"id"`
		} `json:"invite"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &invRevoke)
	rec = doReq(t, h, http.MethodDelete, "/api/v1/projects/audit-shop/invites/"+strconv.FormatInt(invRevoke.Invite.ID, 10), nil, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("invite.revoke = %d", rec.Code)
	}

	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/audit-shop/invites", map[string]any{
		"email": "member@audit.test", "role": "viewer",
	}, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite.create(member) = %d", rec.Code)
	}
	var invMem struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &invMem)
	memInvTok := invMem.URL[strings.LastIndex(invMem.URL, "/")+1:]
	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+memInvTok+"/accept", map[string]any{
		"name": "Member M", "password": pass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept member invite = %d; body=%s", rec.Code, rec.Body.String())
	}
	memUser, err := env.store.GetUserByEmail(t.Context(), "member@audit.test")
	if err != nil {
		t.Fatalf("GetUserByEmail(member): %v", err)
	}

	rec = doReq(t, h, http.MethodPatch, "/api/v1/projects/audit-shop/members/"+strconv.FormatInt(memUser.ID, 10), map[string]any{
		"role": "member",
	}, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("member.update = %d", rec.Code)
	}
	rec = doReq(t, h, http.MethodDelete, "/api/v1/projects/audit-shop/members/"+strconv.FormatInt(memUser.ID, 10), nil, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("member.remove = %d", rec.Code)
	}

	// 20-24. smtp_credential.{create,reveal,rotate,update,delete}
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/audit-shop/smtp-credentials", map[string]any{
		"label": "staging",
	}, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("smtp_credential.create = %d", rec.Code)
	}
	var credResp struct {
		Credential struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"credential"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &credResp)
	credBase := "/api/v1/projects/audit-shop/smtp-credentials/" + strconv.FormatInt(credResp.Credential.ID, 10)
	if r := doReq(t, h, http.MethodPost, credBase+"/reveal", nil, ownerCookie, csrfHeaders(ownerCSRF)); r.Code != http.StatusOK {
		t.Fatalf("smtp_credential.reveal = %d", r.Code)
	}
	if r := doReq(t, h, http.MethodPost, credBase+"/rotate", nil, ownerCookie, csrfHeaders(ownerCSRF)); r.Code != http.StatusOK {
		t.Fatalf("smtp_credential.rotate = %d", r.Code)
	}
	if r := doReq(t, h, http.MethodPatch, credBase, map[string]any{"label": "staging-2"}, ownerCookie, csrfHeaders(ownerCSRF)); r.Code != http.StatusOK {
		t.Fatalf("smtp_credential.update = %d", r.Code)
	}

	// 25. smtp.auth_lockout (trigger via real smtpd server)
	smtpCfg, err := config.Load(func(k string) (string, bool) {
		m := map[string]string{
			"MAILCUBBY_MODE":                     "server",
			"MAILCUBBY_DATA_DIR":                 t.TempDir(),
			"MAILCUBBY_BASE_URL":                 testBaseURL,
			"MAILCUBBY_APP_KEY":                  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			"MAILCUBBY_SMTP_ALLOW_INSECURE_AUTH": "true",
			"MAILCUBBY_SMTP_AUTH_MAX_FAILURES":   "2",
			"MAILCUBBY_SMTP_AUTH_LOCKOUT":        "15m",
		}
		v, ok := m[k]
		return v, ok
	})
	if err != nil {
		t.Fatalf("config.Load(smtp): %v", err)
	}
	smtpSrv := smtpd.New(smtpCfg, env.store, env.ing, env.clk, nil)
	smtpLn := listenLoopback(t)
	go func() { _ = smtpSrv.Serve(smtpLn) }()
	t.Cleanup(func() { _ = smtpSrv.Close() })
	triggerSMTPLockout(t, smtpLn.Addr().String(), credResp.Credential.Username, env.clk)

	if r := doReq(t, h, http.MethodDelete, credBase, nil, ownerCookie, csrfHeaders(ownerCSRF)); r.Code != http.StatusNoContent {
		t.Fatalf("smtp_credential.delete = %d", r.Code)
	}

	// 26. api_token.create & 27. api_token.revoke
	rec = doReq(t, h, http.MethodPost, "/api/v1/tokens", map[string]any{
		"name": "Audit Token", "scope": "read",
	}, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("api_token.create = %d", rec.Code)
	}
	var tokOut struct {
		TokenInfo struct {
			ID int64 `json:"id"`
		} `json:"token_info"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tokOut)
	if r := doReq(t, h, http.MethodDelete, "/api/v1/tokens/"+strconv.FormatInt(tokOut.TokenInfo.ID, 10), nil, ownerCookie, csrfHeaders(ownerCSRF)); r.Code != http.StatusNoContent {
		t.Fatalf("api_token.revoke = %d", r.Code)
	}

	// 28. messages.delete_all
	if r := doReq(t, h, http.MethodPost, "/api/v1/projects/audit-shop/send-test", nil, ownerCookie, csrfHeaders(ownerCSRF)); r.Code != http.StatusCreated {
		t.Fatalf("send-test = %d", r.Code)
	}
	if r := doReq(t, h, http.MethodDelete, "/api/v1/projects/audit-shop/messages", nil, ownerCookie, csrfHeaders(ownerCSRF)); r.Code != http.StatusOK {
		t.Fatalf("messages.delete_all = %d", r.Code)
	}

	// 29. settings.update
	if r := doReq(t, h, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"instance_name": "Audit Updated",
	}, adminCookie, csrfHeaders(adminCSRF)); r.Code != http.StatusOK {
		t.Fatalf("settings.update = %d", r.Code)
	}

	// 30. project.delete (create & delete disposable project so audit-shop remains for project audit tests)
	if r := doReq(t, h, http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Disposable", "slug": "disposable-audit",
	}, ownerCookie, csrfHeaders(ownerCSRF)); r.Code != http.StatusCreated {
		t.Fatalf("create disposable project = %d", r.Code)
	}
	if r := doReq(t, h, http.MethodDelete, "/api/v1/projects/disposable-audit", map[string]any{
		"confirm_slug": "disposable-audit",
	}, ownerCookie, csrfHeaders(ownerCSRF)); r.Code != http.StatusNoContent {
		t.Fatalf("project.delete = %d", r.Code)
	}

	// 31. auth.logout (owner logs out; admin stays signed in to query audit API)
	if r := doReq(t, h, http.MethodPost, "/api/v1/auth/logout", nil, ownerCookie, csrfHeaders(ownerCSRF)); r.Code != http.StatusNoContent {
		t.Fatalf("auth.logout = %d", r.Code)
	}

	// Collect all audit entries through paginated GET /api/v1/admin/audit (limit=10).
	seenActions := make(map[string]int)
	var cursor string
	pages := 0
	for {
		path := "/api/v1/admin/audit?limit=10"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := doReq(t, h, http.MethodGet, path, nil, adminCookie, nil)
		if r.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body=%s", path, r.Code, r.Body.String())
		}
		var page auditListDTO
		if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
			t.Fatalf("unmarshal admin audit page: %v", err)
		}
		pages++
		for _, item := range page.Items {
			seenActions[item.Action]++
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if pages < 3 {
		t.Fatalf("expected multi-page audit log (>=3 pages with limit=10), got %d", pages)
	}

	for _, action := range allFR105Actions {
		if seenActions[action] == 0 {
			t.Errorf("FR-105 audit action %q was never recorded", action)
		}
	}

	// Verify admin audit filters: action, project, and user.
	r := doReq(t, h, http.MethodGet, "/api/v1/admin/audit?action=messages.delete_all&project=audit-shop&user=owner@audit.test", nil, adminCookie, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("GET filtered admin audit = %d; body=%s", r.Code, r.Body.String())
	}
	var filtered auditListDTO
	if err := json.Unmarshal(r.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("unmarshal filtered admin audit: %v", err)
	}
	if len(filtered.Items) != 1 {
		t.Fatalf("expected 1 filtered messages.delete_all entry, got %d", len(filtered.Items))
	}
	if filtered.Items[0].ProjectSlug == nil || *filtered.Items[0].ProjectSlug != "audit-shop" {
		t.Fatalf("expected project_slug=audit-shop, got %v", filtered.Items[0].ProjectSlug)
	}

	// Verify owner project audit endpoint with action filter and pagination.
	ownerCookie2, _ := loginAndGetCSRF(t, h, "owner@audit.test", pass2)
	r = doReq(t, h, http.MethodGet, "/api/v1/projects/audit-shop/audit?action=smtp_credential.reveal", nil, ownerCookie2, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("GET project audit = %d; body=%s", r.Code, r.Body.String())
	}
	var projAudit auditListDTO
	if err := json.Unmarshal(r.Body.Bytes(), &projAudit); err != nil {
		t.Fatalf("unmarshal project audit: %v", err)
	}
	if len(projAudit.Items) != 1 || projAudit.Items[0].Action != "smtp_credential.reveal" {
		t.Fatalf("expected 1 smtp_credential.reveal entry, got %+v", projAudit.Items)
	}

	// Verify nonexistent project filter on admin audit cleanly returns empty list.
	r = doReq(t, h, http.MethodGet, "/api/v1/admin/audit?project=no-such-slug", nil, adminCookie, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("GET admin audit nonexistent project = %d, want 200", r.Code)
	}
	var emptyAudit auditListDTO
	_ = json.Unmarshal(r.Body.Bytes(), &emptyAudit)
	if len(emptyAudit.Items) != 0 {
		t.Fatalf("expected 0 items for nonexistent project filter, got %d", len(emptyAudit.Items))
	}

	// Verify invalid cursor/limit return 400.
	if r := doReq(t, h, http.MethodGet, "/api/v1/admin/audit?limit=999", nil, adminCookie, nil); r.Code != http.StatusBadRequest {
		t.Fatalf("limit=999 status = %d, want 400", r.Code)
	}
	if r := doReq(t, h, http.MethodGet, "/api/v1/projects/audit-shop/audit?cursor=not-a-cursor", nil, ownerCookie2, nil); r.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor status = %d, want 400", r.Code)
	}

	// Ensure store.AuditActorSMTP is referenced cleanly.
	_ = store.AuditActorSMTP
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen loopback: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

type insecurePlainAuth struct {
	user  string
	value string
}

func (a insecurePlainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.value), nil
}

func (insecurePlainAuth) Next([]byte, bool) ([]byte, error) { return nil, nil }

func triggerSMTPLockout(t *testing.T, addr, username string, clk *clock.Fake) {
	t.Helper()
	for attempt := range 2 {
		done := make(chan error, 1)
		go func() {
			c, err := smtp.Dial(addr)
			if err != nil {
				done <- err
				return
			}
			defer func() { _ = c.Close() }()
			done <- c.Auth(insecurePlainAuth{user: username, value: "bad-attempt"})
		}()

		// Advance fake clock past the 1s auth failure delay once the timer is registered.
		ticker := time.NewTicker(5 * time.Millisecond)
		deadline := time.NewTimer(2 * time.Second)
		for {
			select {
			case err := <-done:
				ticker.Stop()
				deadline.Stop()
				if err == nil {
					t.Fatalf("attempt %d: expected SMTP AUTH error, got nil", attempt+1)
				}
				goto nextAttempt
			case <-ticker.C:
				clk.Advance(time.Second)
			case <-deadline.C:
				ticker.Stop()
				t.Fatalf("attempt %d: timed out waiting for SMTP AUTH failure", attempt+1)
			}
		}
	nextAttempt:
	}
}
