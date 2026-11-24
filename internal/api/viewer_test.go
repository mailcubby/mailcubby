package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/store"
)

// FR-050, FR-071, FR-075: viewer cannot mark seen/unseen, cannot fetch raw/eml/html/headers
// when viewer_hide_technical is on (403) but can when off (200), and message detail omits
// envelope, headers, and parse_warnings for restricted viewers.
func TestViewerTechnicalAndSeenRestrictions(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	owner := env.createUser(t, "owner@example.test", "Owner", pass, false, true, false)
	member := env.createUser(t, "member@example.test", "Member", pass, false, false, false)
	viewer := env.createUser(t, "viewer@example.test", "Viewer", pass, false, false, false)

	// DefaultProjectSettings has ViewerHideTechnical = true.
	proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "client-app", Name: "Client App", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for _, m := range []struct {
		uid  int64
		role string
	}{
		{owner.ID, "owner"},
		{member.ID, "member"},
		{viewer.ID, "viewer"},
	} {
		if _, err := env.store.AddProjectMember(t.Context(), proj.ID, m.uid, m.role, env.clk.Now().UTC()); err != nil {
			t.Fatalf("AddProjectMember: %v", err)
		}
	}

	ownerCookie, ownerCSRF := loginAndGetCSRF(t, h, owner.Email, pass)
	memberCookie, _ := loginAndGetCSRF(t, h, member.Email, pass)
	viewerCookie, viewerCSRF := loginAndGetCSRF(t, h, viewer.Email, pass)

	// Ingest the built-in sample message (HTML + text + inline PNG + PDF attachment).
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects/client-app/send-test", nil, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("send-test status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var sent struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sent); err != nil {
		t.Fatalf("unmarshal send-test: %v", err)
	}

	msgBase := "/api/v1/projects/client-app/messages/" + sent.ID
	techPaths := []string{msgBase + "/raw", msgBase + "/eml", msgBase + "/html", msgBase + "/headers"}

	// 1. Viewer cannot mark seen via PATCH or bulk (403); message remains unseen.
	rec = doReq(t, h, http.MethodPatch, msgBase, map[string]any{"seen": true}, viewerCookie, csrfHeaders(viewerCSRF))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer PATCH seen status = %d, want 403", rec.Code)
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/client-app/messages/bulk", map[string]any{
		"ids":    []string{sent.ID},
		"action": "seen",
	}, viewerCookie, csrfHeaders(viewerCSRF))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer bulk seen status = %d, want 403", rec.Code)
	}

	// 2. With viewer_hide_technical=true: viewer gets 403 on raw/eml/html/headers.
	for _, p := range techPaths {
		rec = doReq(t, h, http.MethodGet, p, nil, viewerCookie, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("viewer (hide=true) GET %s status = %d, want 403", p, rec.Code)
		}
	}

	// Viewer CAN still view render preview and attachments, and message detail omits technical keys.
	rec = doReq(t, h, http.MethodGet, "/render/client-app/"+sent.ID, nil, viewerCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer GET /render status = %d, want 200", rec.Code)
	}

	rec = doReq(t, h, http.MethodGet, msgBase, nil, viewerCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer GET message detail status = %d, want 200", rec.Code)
	}
	var viewerDetail map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &viewerDetail); err != nil {
		t.Fatalf("unmarshal viewer detail: %v", err)
	}
	for _, forbiddenKey := range []string{"envelope", "headers", "parse_warnings"} {
		if _, exists := viewerDetail[forbiddenKey]; exists {
			t.Fatalf("viewer (hide=true) message detail must omit %q, got %v", forbiddenKey, viewerDetail[forbiddenKey])
		}
	}
	if seen, _ := viewerDetail["seen"].(bool); seen {
		t.Fatalf("opening message as viewer must not mark it seen")
	}

	// Viewer can download attachments even when viewer_hide_technical=true.
	atts, ok := viewerDetail["attachments"].([]any)
	if !ok || len(atts) == 0 {
		t.Fatalf("expected attachments in sample message detail, got %v", viewerDetail["attachments"])
	}
	attMap := atts[0].(map[string]any)
	attID := int64(attMap["id"].(float64))
	rec = doReq(t, h, http.MethodGet, msgBase+"/attachments/"+strconv.FormatInt(attID, 10), nil, viewerCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer GET attachment status = %d, want 200", rec.Code)
	}

	// Member sees technical fields and endpoints even when viewer_hide_technical=true.
	rec = doReq(t, h, http.MethodGet, msgBase, nil, memberCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("member GET message detail status = %d, want 200", rec.Code)
	}
	var memberDetail map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &memberDetail); err != nil {
		t.Fatalf("unmarshal member detail: %v", err)
	}
	for _, techKey := range []string{"envelope", "headers", "parse_warnings"} {
		if _, exists := memberDetail[techKey]; !exists {
			t.Fatalf("member message detail missing %q", techKey)
		}
	}
	for _, p := range techPaths {
		rec = doReq(t, h, http.MethodGet, p, nil, memberCookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("member GET %s status = %d, want 200", p, rec.Code)
		}
	}

	// 3. Owner turns viewer_hide_technical=false -> viewer now sees technical endpoints and fields.
	rec = doReq(t, h, http.MethodPatch, "/api/v1/projects/client-app", map[string]any{
		"settings": map[string]any{
			"viewer_hide_technical": false,
		},
	}, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("owner PATCH viewer_hide_technical=false status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	for _, p := range techPaths {
		rec = doReq(t, h, http.MethodGet, p, nil, viewerCookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("viewer (hide=false) GET %s status = %d, want 200; body=%s", p, rec.Code, rec.Body.String())
		}
	}
	rec = doReq(t, h, http.MethodGet, msgBase, nil, viewerCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer (hide=false) GET message detail status = %d, want 200", rec.Code)
	}
	var unlockedDetail map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &unlockedDetail); err != nil {
		t.Fatalf("unmarshal unlocked viewer detail: %v", err)
	}
	for _, techKey := range []string{"envelope", "headers", "parse_warnings"} {
		if _, exists := unlockedDetail[techKey]; !exists {
			t.Fatalf("viewer (hide=false) message detail missing %q", techKey)
		}
	}

	// FR-050: even with viewer_hide_technical=false, envelope.auth_user (SMTP username)
	// must be omitted/null for viewers while visible to members.
	authRefs, err := env.ing.Ingest(t.Context(), ingest.Envelope{
		MailFrom: "app@example.test",
		RcptTo:   []string{"user@example.test"},
		AuthUser: "client-app-abc12345",
	}, []ingest.Target{{ProjectID: proj.ID, ProjectSlug: proj.Slug}}, bytes.NewReader([]byte("Subject: AuthUser\r\n\r\nBody\r\n")))
	if err != nil {
		t.Fatalf("Ingest auth message: %v", err)
	}
	authMsgPath := "/api/v1/projects/client-app/messages/" + authRefs[0].ID.String()

	rec = doReq(t, h, http.MethodGet, authMsgPath, nil, viewerCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer GET auth message status = %d, want 200", rec.Code)
	}
	var viewerAuthDetail struct {
		Envelope struct {
			AuthUser *string `json:"auth_user"`
		} `json:"envelope"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &viewerAuthDetail); err != nil {
		t.Fatalf("unmarshal viewer auth detail: %v", err)
	}
	if viewerAuthDetail.Envelope.AuthUser != nil {
		t.Fatalf("viewer saw envelope.auth_user = %q, want nil (FR-050)", *viewerAuthDetail.Envelope.AuthUser)
	}

	rec = doReq(t, h, http.MethodGet, authMsgPath, nil, memberCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("member GET auth message status = %d, want 200", rec.Code)
	}
	var memberAuthDetail struct {
		Envelope struct {
			AuthUser *string `json:"auth_user"`
		} `json:"envelope"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &memberAuthDetail); err != nil {
		t.Fatalf("unmarshal member auth detail: %v", err)
	}
	if memberAuthDetail.Envelope.AuthUser == nil || *memberAuthDetail.Envelope.AuthUser != "client-app-abc12345" {
		t.Fatalf("member envelope.auth_user = %v, want client-app-abc12345", memberAuthDetail.Envelope.AuthUser)
	}
}
