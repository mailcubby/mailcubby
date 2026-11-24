package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/store"
)

func bearerHeaders(token string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + token,
	}
}

// FR-065, FR-066: personal API token CRUD, bearer authentication, effective permission
// rules, project scoping, expiry, revocation, and last_used_at throttling.
func TestAPITokensLifecycleAndPermissions(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	owner := env.createUser(t, "owner@tok.test", "Owner", pass, false, true, false)
	member := env.createUser(t, "member@tok.test", "Member", pass, false, false, false)
	viewer := env.createUser(t, "viewer@tok.test", "Viewer", pass, false, false, false)
	other := env.createUser(t, "other@tok.test", "Other", pass, false, false, false)

	projA, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "tok-a", Name: "Tok A", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject(A): %v", err)
	}
	projB, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "tok-b", Name: "Tok B", Color: "green", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject(B): %v", err)
	}

	for _, m := range []struct {
		pid  int64
		uid  int64
		role string
	}{
		{projA.ID, owner.ID, "owner"},
		{projA.ID, member.ID, "member"},
		{projA.ID, viewer.ID, "viewer"},
		{projB.ID, owner.ID, "owner"},
		{projB.ID, other.ID, "owner"},
	} {
		if _, err := env.store.AddProjectMember(t.Context(), m.pid, m.uid, m.role, env.clk.Now().UTC()); err != nil {
			t.Fatalf("AddProjectMember: %v", err)
		}
	}

	ownerCookie, ownerCSRF := loginAndGetCSRF(t, h, owner.Email, pass)
	memberCookie, memberCSRF := loginAndGetCSRF(t, h, member.Email, pass)
	viewerCookie, viewerCSRF := loginAndGetCSRF(t, h, viewer.Email, pass)
	otherCookie, otherCSRF := loginAndGetCSRF(t, h, other.Email, pass)

	// Seed a message in projA and projB.
	sendSample := func(slug string, cookie *http.Cookie, csrf string) string {
		t.Helper()
		rec := doReq(t, h, http.MethodPost, "/api/v1/projects/"+slug+"/send-test", nil, cookie, csrfHeaders(csrf))
		if rec.Code != http.StatusCreated {
			t.Fatalf("send-test(%s) = %d, want 201", slug, rec.Code)
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal send-test: %v", err)
		}
		return out.ID
	}
	msgA1 := sendSample("tok-a", ownerCookie, ownerCSRF)
	msgA2 := sendSample("tok-a", ownerCookie, ownerCSRF)
	_ = sendSample("tok-b", ownerCookie, ownerCSRF)

	createToken := func(cookie *http.Cookie, csrf string, body map[string]any) (int64, string, string) {
		t.Helper()
		rec := doReq(t, h, http.MethodPost, "/api/v1/tokens", body, cookie, csrfHeaders(csrf))
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /tokens status = %d, want 201; body=%s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
			t.Errorf("POST /tokens Cache-Control = %q, want private, no-store", got)
		}
		var out struct {
			TokenInfo struct {
				ID     int64  `json:"id"`
				Prefix string `json:"prefix"`
			} `json:"token_info"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal token: %v", err)
		}
		if !strings.HasPrefix(out.Token, auth.APITokenPrefix) || len(out.Token) != len(auth.APITokenPrefix)+auth.APITokenBodyLen {
			t.Fatalf("unexpected token format: %q", out.Token)
		}
		if out.TokenInfo.Prefix != out.Token[len(auth.APITokenPrefix):len(auth.APITokenPrefix)+auth.APITokenPrefixLen] {
			t.Fatalf("prefix mismatch: got %q, raw %q", out.TokenInfo.Prefix, out.Token)
		}
		return out.TokenInfo.ID, out.TokenInfo.Prefix, out.Token
	}

	// 1. Validation errors & inaccessible project -> 404.
	rec := doReq(t, h, http.MethodPost, "/api/v1/tokens", map[string]any{
		"name": "", "scope": "owner", "expires_in_days": 0,
	}, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid create token status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/tokens", map[string]any{
		"name": "No access", "scope": "read", "project": "tok-a",
	}, otherCookie, csrfHeaders(otherCSRF))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("create token for inaccessible project status = %d, want 404", rec.Code)
	}

	// 2. Owner creates a read token (unrestricted) and a write token (restricted to tok-a, expires in 7 days).
	readTokID, readPrefix, readTok := createToken(ownerCookie, ownerCSRF, map[string]any{
		"name": "CI Read", "scope": "read",
	})
	writeTokID, _, writeTok := createToken(ownerCookie, ownerCSRF, map[string]any{
		"name": "CI Write A", "scope": "write", "project": "tok-a", "expires_in_days": 7,
	})
	_ = writeTokID

	// GET /tokens lists both tokens without raw secret.
	rec = doReq(t, h, http.MethodGet, "/api/v1/tokens", nil, ownerCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tokens status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), readTok) || strings.Contains(rec.Body.String(), writeTok) {
		t.Fatal("GET /tokens must never return raw token secrets")
	}

	// 3. Read token can list, wait (existing message via since), get detail (with technical fields),
	//    download raw, but DELETE returns 403 and /members returns 403.
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tok-a/messages", nil, nil, bearerHeaders(readTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("read token list messages = %d, want 200", rec.Code)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tok-a/messages/wait?since=00000000000000000000000000&timeout=1s", nil, nil, bearerHeaders(readTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("read token wait = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tok-a/messages/"+msgA1+"/raw", nil, nil, bearerHeaders(readTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("owner read token /raw = %d, want 200 (FR-066)", rec.Code)
	}
	// GET /projects/{p} with read token exposes non-owner settings only.
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tok-a", nil, nil, bearerHeaders(readTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("read token GET project = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "members_can_reveal_smtp") {
		t.Errorf("read token GET project leaked owner-only settings: %s", rec.Body.String())
	}
	// DELETE message with read token -> 403 (no Origin/CSRF needed for bearer).
	rec = doReq(t, h, http.MethodDelete, "/api/v1/projects/tok-a/messages/"+msgA1, nil, nil, bearerHeaders(readTok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read token DELETE message = %d, want 403", rec.Code)
	}
	// Any token on /members -> 403.
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tok-a/members", nil, nil, bearerHeaders(readTok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read token GET /members = %d, want 403", rec.Code)
	}
	// GET /auth/me returns token info block.
	rec = doReq(t, h, http.MethodGet, "/api/v1/auth/me", nil, nil, bearerHeaders(readTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("read token GET /auth/me = %d, want 200", rec.Code)
	}
	var meResp struct {
		CSRFToken string `json:"csrf_token"`
		Token     *struct {
			Scope   string  `json:"scope"`
			Project *string `json:"project"`
		} `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meResp); err != nil {
		t.Fatalf("unmarshal /auth/me: %v", err)
	}
	if meResp.CSRFToken != "" || meResp.Token == nil || meResp.Token.Scope != "read" || meResp.Token.Project != nil {
		t.Fatalf("unexpected /auth/me token response: %+v", meResp)
	}

	// 4. last_used_at is recorded on first use and throttled within 1 minute.
	firstUsed := env.clk.Now().UTC()
	storedTok, err := env.store.GetAPITokenByPrefix(t.Context(), readPrefix)
	if err != nil || storedTok.LastUsedAt == nil || !storedTok.LastUsedAt.Equal(firstUsed) {
		t.Fatalf("expected LastUsedAt=%v after first use, got %v (err=%v)", firstUsed, storedTok.LastUsedAt, err)
	}
	env.clk.Advance(30 * time.Second)
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects", nil, nil, bearerHeaders(readTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("read token GET /projects = %d, want 200", rec.Code)
	}
	storedTok, _ = env.store.GetAPITokenByPrefix(t.Context(), readPrefix)
	if !storedTok.LastUsedAt.Equal(firstUsed) {
		t.Errorf("LastUsedAt updated within 1m window: got %v, want %v", storedTok.LastUsedAt, firstUsed)
	}
	env.clk.Advance(31 * time.Second)
	secondUsed := env.clk.Now().UTC()
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects", nil, nil, bearerHeaders(readTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("read token GET /projects (after 1m) = %d, want 200", rec.Code)
	}
	storedTok, _ = env.store.GetAPITokenByPrefix(t.Context(), readPrefix)
	if !storedTok.LastUsedAt.Equal(secondUsed) {
		t.Errorf("LastUsedAt not updated after 1m: got %v, want %v", storedTok.LastUsedAt, secondUsed)
	}

	// 5. Write token (project-restricted to tok-a) can delete in tok-a without Origin/CSRF,
	//    cannot access tok-b (404), only sees tok-a in /projects and /auth/me, and gets 403 on /members and PATCH project.
	rec = doReq(t, h, http.MethodDelete, "/api/v1/projects/tok-a/messages/"+msgA1, nil, nil, bearerHeaders(writeTok))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("write token DELETE message = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tok-b/messages", nil, nil, bearerHeaders(writeTok))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("project-restricted token accessing tok-b = %d, want 404", rec.Code)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects", nil, nil, bearerHeaders(writeTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("write token GET /projects = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"tok-b"`) || !strings.Contains(rec.Body.String(), `"tok-a"`) {
		t.Errorf("project-restricted token GET /projects should contain only tok-a: %s", rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tok-a/members", nil, nil, bearerHeaders(writeTok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("write token GET /members = %d, want 403", rec.Code)
	}
	rec = doReq(t, h, http.MethodPatch, "/api/v1/projects/tok-a", map[string]any{"name": "Nope"}, nil, bearerHeaders(writeTok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("write token PATCH project = %d, want 403", rec.Code)
	}

	// 6. FR-066 technical views nuance:
	//    - member's read token CAN fetch /raw and sees technical fields in detail even when viewer_hide_technical=true.
	//    - viewer's write token is capped to viewer: CANNOT delete (403), CANNOT fetch /raw (403), omits technical fields.
	_, _, memberReadTok := createToken(memberCookie, memberCSRF, map[string]any{
		"name": "Member Read", "scope": "read",
	})
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tok-a/messages/"+msgA2+"/raw", nil, nil, bearerHeaders(memberReadTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("member read token /raw = %d, want 200 (FR-066)", rec.Code)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tok-a/messages/"+msgA2, nil, nil, bearerHeaders(memberReadTok))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"envelope"`) {
		t.Fatalf("member read token message detail should include envelope: status=%d body=%s", rec.Code, rec.Body.String())
	}

	_, _, viewerWriteTok := createToken(viewerCookie, viewerCSRF, map[string]any{
		"name": "Viewer Write", "scope": "write",
	})
	rec = doReq(t, h, http.MethodDelete, "/api/v1/projects/tok-a/messages/"+msgA2, nil, nil, bearerHeaders(viewerWriteTok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer write token DELETE = %d, want 403 (capped to viewer)", rec.Code)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tok-a/messages/"+msgA2+"/raw", nil, nil, bearerHeaders(viewerWriteTok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer write token /raw = %d, want 403 (viewer_hide_technical=true)", rec.Code)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tok-a/messages/"+msgA2, nil, nil, bearerHeaders(viewerWriteTok))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"envelope"`) {
		t.Fatalf("viewer write token message detail must omit envelope: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 7. Project-restricted token SSE receives tok-a events only, not tok-b.
	sseCtx, cancelSSE := context.WithCancel(t.Context())
	defer cancelSSE()
	sseReq := httptest.NewRequestWithContext(sseCtx, http.MethodGet, "/api/v1/events", nil)
	sseReq.Header.Set("Authorization", "Bearer "+writeTok)
	sw := newSSEStreamWriter()
	sseDone := make(chan struct{})
	go func() {
		defer close(sseDone)
		h.ServeHTTP(sw, sseReq)
	}()
	sw.waitForContains(t, "retry: 3000")
	_ = sendSample("tok-b", ownerCookie, ownerCSRF)
	_ = sendSample("tok-a", ownerCookie, ownerCSRF)
	sw.waitForContains(t, `"project":"tok-a"`)
	if strings.Contains(sw.String(), `"project":"tok-b"`) {
		t.Fatalf("project-restricted token SSE leaked tok-b event: %s", sw.String())
	}
	cancelSSE()
	<-sseDone

	// 8. Revoking a token makes it immediately return 401 on both GET and unsafe DELETE (without Origin).
	//    Also verify bearer header does NOT silently fall back to an attached session cookie.
	rec = doReq(t, h, http.MethodDelete, "/api/v1/tokens/"+strconv.FormatInt(readTokID, 10), nil, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /tokens/{id} = %d, want 204", rec.Code)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tok-a/messages", nil, ownerCookie, bearerHeaders(readTok))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token GET (even with cookie) = %d, want 401", rec.Code)
	}
	rec = doReq(t, h, http.MethodDelete, "/api/v1/projects/tok-a/messages/"+msgA2, nil, nil, bearerHeaders(readTok))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token DELETE without Origin = %d, want 401", rec.Code)
	}

	// 9. Expired token returns 401 after advancing fake clock past 7 days.
	env.clk.Advance(8 * 24 * time.Hour)
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/tok-a/messages", nil, nil, bearerHeaders(writeTok))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired token GET = %d, want 401", rec.Code)
	}

	// 10. Audit entries for api_token.create and api_token.revoke exist and never contain raw token.
	audits, err := env.store.ListAuditEntries(t.Context(), 50)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	var sawCreate, sawRevoke bool
	for _, a := range audits {
		if a.Action == "api_token.create" {
			sawCreate = true
		}
		if a.Action == "api_token.revoke" {
			sawRevoke = true
		}
		if a.MetadataJSON != nil && (strings.Contains(*a.MetadataJSON, readTok) || strings.Contains(*a.MetadataJSON, writeTok)) {
			t.Fatalf("audit metadata leaked raw token: %s", *a.MetadataJSON)
		}
	}
	if !sawCreate || !sawRevoke {
		t.Errorf("expected api_token.create (%v) and api_token.revoke (%v) audit entries", sawCreate, sawRevoke)
	}
}
