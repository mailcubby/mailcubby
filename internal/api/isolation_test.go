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

// ARCHITECTURE section 7 item 4, SPEC 12.5: two-tenant isolation integration test.
// Seeds projects A and B with messages, attachments, tags, members, and invites;
// as owner of A only, asserts 404 for every route using B's slug or B's IDs
// substituted into A's paths, and zero leaks across list responses and SSE.
func TestTwoTenantIsolation(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	userA := env.createUser(t, "ownera@tenant-a.test", "Owner A", pass, false, true, false)
	memberA := env.createUser(t, "membera@tenant-a.test", "Member A", pass, false, false, false)
	userB := env.createUser(t, "ownerb@tenant-b.test", "Owner B", pass, false, true, false)
	memberB := env.createUser(t, "memberb@tenant-b.test", "Member B", pass, false, false, false)

	projA, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "tenant-a", Name: "Tenant A", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject(A): %v", err)
	}
	projB, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "tenant-b", Name: "Tenant B", Color: "red", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject(B): %v", err)
	}

	for _, m := range []struct {
		pid  int64
		uid  int64
		role string
	}{
		{projA.ID, userA.ID, "owner"},
		{projA.ID, memberA.ID, "member"},
		{projB.ID, userB.ID, "owner"},
		{projB.ID, memberB.ID, "member"},
	} {
		if _, err := env.store.AddProjectMember(t.Context(), m.pid, m.uid, m.role, env.clk.Now().UTC()); err != nil {
			t.Fatalf("AddProjectMember: %v", err)
		}
	}

	cookieA, csrfA := loginAndGetCSRF(t, h, userA.Email, pass)
	cookieB, csrfB := loginAndGetCSRF(t, h, userB.Email, pass)

	seedTenantResources := func(slug string, cookie *http.Cookie, csrf string, tagName, invEmail, credLabel string) (msgID string, attID, pdfAttID, tagID, invID, credID int64, credUsername string) {
		t.Helper()
		rec := doReq(t, h, http.MethodPost, "/api/v1/projects/"+slug+"/send-test", nil, cookie, csrfHeaders(csrf))
		if rec.Code != http.StatusCreated {
			t.Fatalf("send-test(%s) status = %d, want 201", slug, rec.Code)
		}
		var sent struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &sent)
		msgID = sent.ID

		detRec := doReq(t, h, http.MethodGet, "/api/v1/projects/"+slug+"/messages/"+msgID, nil, cookie, nil)
		var det struct {
			Attachments []struct {
				ID          int64  `json:"id"`
				ContentType string `json:"content_type"`
			} `json:"attachments"`
		}
		_ = json.Unmarshal(detRec.Body.Bytes(), &det)
		if len(det.Attachments) == 0 {
			t.Fatalf("expected attachment in sample message for %s", slug)
		}
		attID = det.Attachments[0].ID
		for _, a := range det.Attachments {
			if strings.HasPrefix(strings.ToLower(a.ContentType), "application/pdf") {
				pdfAttID = a.ID
				break
			}
		}
		if pdfAttID == 0 {
			t.Fatalf("expected PDF attachment in sample message for %s", slug)
		}

		tagRec := doReq(t, h, http.MethodPost, "/api/v1/projects/"+slug+"/tags", map[string]any{"name": tagName}, cookie, csrfHeaders(csrf))
		if tagRec.Code != http.StatusCreated {
			t.Fatalf("create tag(%s) status = %d, want 201", slug, tagRec.Code)
		}
		var tg struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(tagRec.Body.Bytes(), &tg)
		tagID = tg.ID

		invRec := doReq(t, h, http.MethodPost, "/api/v1/projects/"+slug+"/invites", map[string]any{
			"email": invEmail, "role": "viewer",
		}, cookie, csrfHeaders(csrf))
		if invRec.Code != http.StatusCreated {
			t.Fatalf("create invite(%s) status = %d, want 201", slug, invRec.Code)
		}
		var inv struct {
			Invite struct {
				ID int64 `json:"id"`
			} `json:"invite"`
		}
		_ = json.Unmarshal(invRec.Body.Bytes(), &inv)
		invID = inv.Invite.ID

		credRec := doReq(t, h, http.MethodPost, "/api/v1/projects/"+slug+"/smtp-credentials", map[string]any{
			"label": credLabel,
		}, cookie, csrfHeaders(csrf))
		if credRec.Code != http.StatusCreated {
			t.Fatalf("create smtp credential(%s) status = %d, want 201", slug, credRec.Code)
		}
		var cred struct {
			Credential struct {
				ID       int64  `json:"id"`
				Username string `json:"username"`
			} `json:"credential"`
		}
		_ = json.Unmarshal(credRec.Body.Bytes(), &cred)
		credID = cred.Credential.ID
		credUsername = cred.Credential.Username
		return
	}

	msgA, _, _, _, _, _, _ := seedTenantResources("tenant-a", cookieA, csrfA, "tag-alpha", "inva@tenant-a.test", "cred-alpha")
	msgB, attB, pdfAttB, tagB, invB, credB, credUserB := seedTenantResources("tenant-b", cookieB, csrfB, "tag-beta", "invb@tenant-b.test", "cred-beta")

	// 1. Direct access by User A to Tenant B's slug must return 404 across all routes.
	bRoutes := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/v1/projects/tenant-b", nil},
		{http.MethodPatch, "/api/v1/projects/tenant-b", map[string]any{"name": "Hijacked"}},
		{http.MethodDelete, "/api/v1/projects/tenant-b", map[string]any{"confirm_slug": "tenant-b"}},
		{http.MethodPost, "/api/v1/projects/tenant-b/leave", nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/audit", nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/messages", nil},
		{http.MethodDelete, "/api/v1/projects/tenant-b/messages", nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/messages/wait?timeout=1s", nil},
		{http.MethodPost, "/api/v1/projects/tenant-b/messages/bulk", map[string]any{"ids": []string{msgB}, "action": "delete"}},
		{http.MethodGet, "/api/v1/projects/tenant-b/messages/" + msgB, nil},
		{http.MethodPatch, "/api/v1/projects/tenant-b/messages/" + msgB, map[string]any{"seen": true}},
		{http.MethodDelete, "/api/v1/projects/tenant-b/messages/" + msgB, nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/messages/" + msgB + "/raw", nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/messages/" + msgB + "/eml", nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/messages/" + msgB + "/html", nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/messages/" + msgB + "/html-check", nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/messages/" + msgB + "/links", nil},
		{http.MethodPost, "/api/v1/projects/tenant-b/messages/" + msgB + "/links/check", nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/messages/" + msgB + "/headers", nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/messages/" + msgB + "/attachments/" + strconv.FormatInt(attB, 10), nil},
		{http.MethodPost, "/api/v1/projects/tenant-b/send-test", nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/tags", nil},
		{http.MethodPost, "/api/v1/projects/tenant-b/tags", map[string]any{"name": "pwn"}},
		{http.MethodPatch, "/api/v1/projects/tenant-b/tags/" + strconv.FormatInt(tagB, 10), map[string]any{"color": "pink"}},
		{http.MethodDelete, "/api/v1/projects/tenant-b/tags/" + strconv.FormatInt(tagB, 10), nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/members", nil},
		{http.MethodPatch, "/api/v1/projects/tenant-b/members/" + strconv.FormatInt(memberB.ID, 10), map[string]any{"role": "viewer"}},
		{http.MethodDelete, "/api/v1/projects/tenant-b/members/" + strconv.FormatInt(memberB.ID, 10), nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/invites", nil},
		{http.MethodPost, "/api/v1/projects/tenant-b/invites", map[string]any{"email": "x@y.test", "role": "viewer"}},
		{http.MethodDelete, "/api/v1/projects/tenant-b/invites/" + strconv.FormatInt(invB, 10), nil},
		{http.MethodGet, "/api/v1/projects/tenant-b/smtp-credentials", nil},
		{http.MethodPost, "/api/v1/projects/tenant-b/smtp-credentials", map[string]any{"label": "pwn"}},
		{http.MethodPost, "/api/v1/projects/tenant-b/smtp-credentials/" + strconv.FormatInt(credB, 10) + "/reveal", nil},
		{http.MethodPost, "/api/v1/projects/tenant-b/smtp-credentials/" + strconv.FormatInt(credB, 10) + "/rotate", nil},
		{http.MethodPatch, "/api/v1/projects/tenant-b/smtp-credentials/" + strconv.FormatInt(credB, 10), map[string]any{"label": "pwn"}},
		{http.MethodDelete, "/api/v1/projects/tenant-b/smtp-credentials/" + strconv.FormatInt(credB, 10), nil},
		{http.MethodGet, "/api/v1/events?project=tenant-b", nil},
		{http.MethodGet, "/render/tenant-b/" + msgB, nil},
		{http.MethodGet, "/render/tenant-b/" + msgB + "/attachments/" + strconv.FormatInt(pdfAttB, 10), nil},
	}

	for _, tc := range bRoutes {
		var hdrs map[string]string
		if auth.IsUnsafeMethod(tc.method) {
			hdrs = csrfHeaders(csrfA)
		}
		rec := doReq(t, h, tc.method, tc.path, tc.body, cookieA, hdrs)
		if rec.Code != http.StatusNotFound {
			t.Errorf("Tenant A accessing Tenant B %s %s: status = %d, want 404; body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	// 2. Cross-tenant ID substitution into Tenant A's paths must return 404.
	subRoutes := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/v1/projects/tenant-a/messages/" + msgB, nil},
		{http.MethodPatch, "/api/v1/projects/tenant-a/messages/" + msgB, map[string]any{"seen": true}},
		{http.MethodDelete, "/api/v1/projects/tenant-a/messages/" + msgB, nil},
		{http.MethodGet, "/api/v1/projects/tenant-a/messages/" + msgB + "/raw", nil},
		{http.MethodGet, "/api/v1/projects/tenant-a/messages/" + msgB + "/eml", nil},
		{http.MethodGet, "/api/v1/projects/tenant-a/messages/" + msgB + "/html", nil},
		{http.MethodGet, "/api/v1/projects/tenant-a/messages/" + msgB + "/html-check", nil},
		{http.MethodGet, "/api/v1/projects/tenant-a/messages/" + msgB + "/links", nil},
		{http.MethodPost, "/api/v1/projects/tenant-a/messages/" + msgB + "/links/check", nil},
		{http.MethodGet, "/api/v1/projects/tenant-a/messages/" + msgB + "/headers", nil},
		{http.MethodGet, "/api/v1/projects/tenant-a/messages/" + msgA + "/attachments/" + strconv.FormatInt(attB, 10), nil},
		{http.MethodGet, "/api/v1/projects/tenant-a/messages/" + msgB + "/attachments/" + strconv.FormatInt(attB, 10), nil},
		{http.MethodGet, "/render/tenant-a/" + msgB, nil},
		{http.MethodGet, "/render/tenant-a/" + msgA + "/attachments/" + strconv.FormatInt(pdfAttB, 10), nil},
		{http.MethodGet, "/render/tenant-a/" + msgB + "/attachments/" + strconv.FormatInt(pdfAttB, 10), nil},
		{http.MethodPatch, "/api/v1/projects/tenant-a/tags/" + strconv.FormatInt(tagB, 10), map[string]any{"name": "stolen"}},
		{http.MethodDelete, "/api/v1/projects/tenant-a/tags/" + strconv.FormatInt(tagB, 10), nil},
		{http.MethodPatch, "/api/v1/projects/tenant-a/members/" + strconv.FormatInt(memberB.ID, 10), map[string]any{"role": "viewer"}},
		{http.MethodDelete, "/api/v1/projects/tenant-a/members/" + strconv.FormatInt(memberB.ID, 10), nil},
		{http.MethodDelete, "/api/v1/projects/tenant-a/invites/" + strconv.FormatInt(invB, 10), nil},
		{http.MethodPost, "/api/v1/projects/tenant-a/smtp-credentials/" + strconv.FormatInt(credB, 10) + "/reveal", nil},
		{http.MethodPost, "/api/v1/projects/tenant-a/smtp-credentials/" + strconv.FormatInt(credB, 10) + "/rotate", nil},
		{http.MethodPatch, "/api/v1/projects/tenant-a/smtp-credentials/" + strconv.FormatInt(credB, 10), map[string]any{"label": "stolen"}},
		{http.MethodDelete, "/api/v1/projects/tenant-a/smtp-credentials/" + strconv.FormatInt(credB, 10), nil},
	}

	for _, tc := range subRoutes {
		var hdrs map[string]string
		if auth.IsUnsafeMethod(tc.method) {
			hdrs = csrfHeaders(csrfA)
		}
		rec := doReq(t, h, tc.method, tc.path, tc.body, cookieA, hdrs)
		if rec.Code != http.StatusNotFound {
			t.Errorf("Tenant A substituting B ID on %s %s: status = %d, want 404; body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	// Bulk delete under Tenant A with Tenant B's message ID must affect 0 rows and leave B intact.
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects/tenant-a/messages/bulk", map[string]any{
		"ids": []string{msgB}, "action": "delete",
	}, cookieA, csrfHeaders(csrfA))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"affected":0`) {
		t.Fatalf("cross-tenant bulk delete = (%d, %s), want (200, affected=0)", rec.Code, rec.Body.String())
	}
	if _, err := env.store.GetMessageByID(t.Context(), projB.ID, msgB); err != nil {
		t.Fatalf("Tenant B message was affected by Tenant A bulk delete: %v", err)
	}

	// User A cannot revoke User B's personal API token (404).
	tokRec := doReq(t, h, http.MethodPost, "/api/v1/tokens", map[string]any{
		"name": "Tenant B token", "scope": "read", "project": "tenant-b",
	}, cookieB, csrfHeaders(csrfB))
	if tokRec.Code != http.StatusCreated {
		t.Fatalf("Tenant B create token = %d, want 201", tokRec.Code)
	}
	var tokB struct {
		TokenInfo struct {
			ID int64 `json:"id"`
		} `json:"token_info"`
	}
	_ = json.Unmarshal(tokRec.Body.Bytes(), &tokB)
	rec = doReq(t, h, http.MethodDelete, "/api/v1/tokens/"+strconv.FormatInt(tokB.TokenInfo.ID, 10), nil, cookieA, csrfHeaders(csrfA))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Tenant A revoking Tenant B token = %d, want 404", rec.Code)
	}

	// 3. List endpoints for User A must never leak Tenant B identifiers.
	listEndpoints := []string{
		"/api/v1/projects",
		"/api/v1/auth/me",
		"/api/v1/projects/tenant-a/messages",
		"/api/v1/projects/tenant-a/tags",
		"/api/v1/projects/tenant-a/members",
		"/api/v1/projects/tenant-a/invites",
		"/api/v1/projects/tenant-a/smtp-credentials",
		"/api/v1/projects/tenant-a/audit",
	}
	forbiddenTokens := []string{"tenant-b", msgB, "tag-beta", "ownerb@tenant-b.test", "memberb@tenant-b.test", "invb@tenant-b.test", "cred-beta", credUserB}
	for _, p := range listEndpoints {
		rec := doReq(t, h, http.MethodGet, p, nil, cookieA, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("Tenant A GET %s status = %d, want 200", p, rec.Code)
		}
		body := rec.Body.String()
		for _, tok := range forbiddenTokens {
			if strings.Contains(body, tok) {
				t.Errorf("Tenant A GET %s leaked Tenant B token %q in body: %s", p, tok, body)
			}
		}
	}

	// 4. SSE stream for User A must never receive any events from Tenant B.
	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sseReq := httptest.NewRequestWithContext(streamCtx, http.MethodGet, "/api/v1/events", nil)
	sseReq.AddCookie(cookieA)
	sw := newSSEStreamWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(sw, sseReq)
	}()
	sw.waitForContains(t, "retry: 3000")

	// Mutate Tenant B while User A is streaming.
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/tenant-b/send-test", nil, cookieB, csrfHeaders(csrfB))
	if rec.Code != http.StatusCreated {
		t.Fatalf("Tenant B send-test status = %d, want 201", rec.Code)
	}
	rec = doReq(t, h, http.MethodPatch, "/api/v1/projects/tenant-b/messages/"+msgB, map[string]any{"seen": true}, cookieB, csrfHeaders(csrfB))
	if rec.Code != http.StatusOK {
		t.Fatalf("Tenant B patch message status = %d, want 200", rec.Code)
	}

	// Trigger a Tenant A event and heartbeat to confirm stream is live and flushed.
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/tenant-a/send-test", nil, cookieA, csrfHeaders(csrfA))
	if rec.Code != http.StatusCreated {
		t.Fatalf("Tenant A send-test status = %d, want 201", rec.Code)
	}
	sw.waitForContains(t, `"project":"tenant-a"`)
	env.clk.Advance(25 * time.Second)
	sw.waitForContains(t, ": ping")

	streamBody := sw.String()
	if strings.Contains(streamBody, "tenant-b") || strings.Contains(streamBody, msgB) {
		t.Fatalf("Tenant A SSE stream leaked Tenant B events:\n%s", streamBody)
	}

	cancel()
	<-done
}
