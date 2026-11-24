package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/store"
)

// FR-044, FR-046, FR-055, FR-056, FR-100, FR-101: admin users CRUD, disable revoking
// sessions and tokens, last-admin protection, password reset link, and admin projects list.
func TestAdminUsersAndProjectsEndpoints(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"
	const newPass = "new-secure-password-for-testing"

	admin := env.createUser(t, "admin@example.test", "Root Admin", pass, true, true, false)
	adminCookie, adminCSRF := loginAndGetCSRF(t, h, admin.Email, pass)

	// 1. Admin creates a new user -> receives instance invite URL -> user sets password and logs in.
	rec := doReq(t, h, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email":               "alice@example.test",
		"name":                "Alice Dev",
		"is_admin":            false,
		"can_create_projects": true,
	}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /admin/users status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var createRes struct {
		User struct {
			ID                int64  `json:"id"`
			Email             string `json:"email"`
			Name              string `json:"name"`
			HasPassword       bool   `json:"has_password"`
			CanCreateProjects bool   `json:"can_create_projects"`
		} `json:"user"`
		InviteURL string `json:"invite_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &createRes); err != nil {
		t.Fatalf("unmarshal create user: %v", err)
	}
	if createRes.User.HasPassword {
		t.Fatalf("admin-created user should have has_password=false")
	}
	aliceID := createRes.User.ID
	instTok := extractInviteToken(t, createRes.InviteURL)

	// Preview instance invite (project omitted, role null).
	rec = doReq(t, h, http.MethodGet, "/api/v1/invites/"+instTok, nil, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /invites/{token} status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"project"`) {
		t.Fatalf("instance invite preview should omit project: %s", rec.Body.String())
	}

	// Alice accepts instance invite by setting a password, then logs in.
	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+instTok+"/accept", map[string]any{
		"password": pass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusOK {
		t.Fatalf("alice accept instance invite status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	aliceCookie, aliceCSRF := loginAndGetCSRF(t, h, "alice@example.test", pass)

	// Non-admin gets 403 on admin endpoints.
	rec = doReq(t, h, http.MethodGet, "/api/v1/admin/users", nil, aliceCookie, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin GET /admin/users status = %d, want 403", rec.Code)
	}

	// Alice creates a project so project_count and GET /admin/projects are populated.
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Alice Shop",
		"slug": "alice-shop",
	}, aliceCookie, csrfHeaders(aliceCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("alice POST /projects status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	// Add a third user to test pagination + q search.
	_ = env.createUser(t, "bob@example.test", "Bob Viewer", pass, false, false, false)

	// 2. Paginated list (limit=2) and search (?q=alice).
	rec = doReq(t, h, http.MethodGet, "/api/v1/admin/users?limit=2", nil, adminCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/users?limit=2 status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var page1 struct {
		Items []struct {
			ID           int64  `json:"id"`
			Email        string `json:"email"`
			ProjectCount int64  `json:"project_count"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page1); err != nil {
		t.Fatalf("unmarshal users page 1: %v", err)
	}
	if len(page1.Items) != 2 || page1.NextCursor == nil {
		t.Fatalf("page1 = (%d items, next=%v), want (2, non-nil)", len(page1.Items), page1.NextCursor)
	}

	rec = doReq(t, h, http.MethodGet, "/api/v1/admin/users?limit=2&cursor="+*page1.NextCursor, nil, adminCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/users page 2 status = %d, want 200", rec.Code)
	}
	var page2 struct {
		Items      []struct{ Email string } `json:"items"`
		NextCursor *string                  `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page2); err != nil {
		t.Fatalf("unmarshal users page 2: %v", err)
	}
	if len(page2.Items) != 1 || page2.NextCursor != nil {
		t.Fatalf("page2 = (%d items, next=%v), want (1, nil)", len(page2.Items), page2.NextCursor)
	}

	rec = doReq(t, h, http.MethodGet, "/api/v1/admin/users?q=ALICE", nil, adminCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/users?q=ALICE status = %d, want 200", rec.Code)
	}
	var filtered struct {
		Items []struct {
			Email        string `json:"email"`
			ProjectCount int64  `json:"project_count"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("unmarshal filtered users: %v", err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].Email != "alice@example.test" || filtered.Items[0].ProjectCount != 1 {
		t.Fatalf("filtered users = %+v, want alice with project_count=1", filtered.Items)
	}

	// 3. Last-admin protection: cannot delete, demote, or disable the only admin.
	adminPath := "/api/v1/admin/users/" + strconv.FormatInt(admin.ID, 10)
	rec = doReq(t, h, http.MethodDelete, adminPath, nil, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete last admin status = %d, want 409", rec.Code)
	}
	rec = doReq(t, h, http.MethodPatch, adminPath, map[string]any{"is_admin": false}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusConflict {
		t.Fatalf("demote last admin status = %d, want 409", rec.Code)
	}
	rec = doReq(t, h, http.MethodPatch, adminPath, map[string]any{"disabled": true}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusConflict {
		t.Fatalf("disable last admin status = %d, want 409", rec.Code)
	}

	// 4. Disabling Alice revokes her sessions AND her API tokens (FR-044, FR-100).
	// Insert an API token row for Alice directly to verify revocation.
	if err := env.store.WithTx(t.Context(), func(tx *store.Tx) error {
		_, err := tx.SQLTx().ExecContext(t.Context(),
			`INSERT INTO api_tokens (user_id, name, prefix, token_hash, scope, created_at) VALUES (?, 'ci', '123456789012', 'deadbeef', 'read', ?)`,
			aliceID, env.clk.Now().UTC().UnixMilli(),
		)
		return err
	}); err != nil {
		t.Fatalf("insert test api_token: %v", err)
	}

	alicePath := "/api/v1/admin/users/" + strconv.FormatInt(aliceID, 10)
	rec = doReq(t, h, http.MethodPatch, alicePath, map[string]any{"disabled": true}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("disable alice status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Alice's existing session is immediately invalid (401).
	rec = doReq(t, h, http.MethodGet, "/api/v1/auth/me", nil, aliceCookie, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled alice GET /auth/me status = %d, want 401", rec.Code)
	}
	// Alice's API token is revoked in DB.
	var unrevoked int
	if err := env.store.ReaderDB().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL`, aliceID,
	).Scan(&unrevoked); err != nil {
		t.Fatalf("query api_tokens: %v", err)
	}
	if unrevoked != 0 {
		t.Fatalf("expected 0 unrevoked api tokens after disable, got %d", unrevoked)
	}

	// Re-enable Alice and test admin password reset link.
	rec = doReq(t, h, http.MethodPatch, alicePath, map[string]any{"disabled": false}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("re-enable alice status = %d, want 200", rec.Code)
	}
	rec = doReq(t, h, http.MethodPost, alicePath+"/password-reset", nil, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /admin/users/{id}/password-reset status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resetRes struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resetRes); err != nil {
		t.Fatalf("unmarshal password reset response: %v", err)
	}
	resetTok := strings.TrimPrefix(resetRes.URL, testBaseURL+"/reset/")
	rec = doReq(t, h, http.MethodPost, "/api/v1/password-reset/"+resetTok, map[string]any{
		"password": newPass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("complete password reset status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	_, _ = loginAndGetCSRF(t, h, "alice@example.test", newPass)

	// 5. GET /api/v1/admin/projects returns all projects with owners and counters (FR-101).
	rec = doReq(t, h, http.MethodGet, "/api/v1/admin/projects", nil, adminCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/projects status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var adminProjs []struct {
		Slug   string `json:"slug"`
		Owners []struct {
			Email string `json:"email"`
		} `json:"owners"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &adminProjs); err != nil {
		t.Fatalf("unmarshal admin projects: %v", err)
	}
	if len(adminProjs) != 1 || adminProjs[0].Slug != "alice-shop" || len(adminProjs[0].Owners) != 1 || adminProjs[0].Owners[0].Email != "alice@example.test" {
		t.Fatalf("unexpected admin projects: %+v", adminProjs)
	}

	// 6. Deleting a non-admin user returns 204.
	rec = doReq(t, h, http.MethodDelete, alicePath, nil, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete alice status = %d, want 204", rec.Code)
	}
}

// FR-102, FR-105: GET/PATCH /api/v1/admin/settings merges DB overrides over env defaults,
// supports clearing overrides with null, audits settings.update, and updates /meta + project retention_defaults.
func TestAdminSettingsAndStats(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "admin-password-12345"
	env.createUser(t, "admin@example.test", "Admin", pass, true, true, false)
	adminCookie, adminCSRF := loginAndGetCSRF(t, h, "admin@example.test", pass)

	// 1. Initial settings come entirely from env defaults.
	rec := doReq(t, h, http.MethodGet, "/api/v1/admin/settings", nil, adminCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/settings status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Settings struct {
			InstanceName         *string `json:"instance_name"`
			RetentionMaxAge      *int64  `json:"retention_max_age"`
			RetentionMaxMessages *int64  `json:"retention_max_messages"`
			RetentionMaxBytes    *int64  `json:"retention_max_bytes"`
		} `json:"settings"`
		Effective struct {
			InstanceName         string `json:"instance_name"`
			RetentionMaxAge      int64  `json:"retention_max_age"`
			RetentionMaxMessages int64  `json:"retention_max_messages"`
			RetentionMaxBytes    int64  `json:"retention_max_bytes"`
		} `json:"effective"`
		Source struct {
			InstanceName         string `json:"instance_name"`
			RetentionMaxAge      string `json:"retention_max_age"`
			RetentionMaxMessages string `json:"retention_max_messages"`
			RetentionMaxBytes    string `json:"retention_max_bytes"`
		} `json:"source"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal initial settings: %v", err)
	}
	if got.Settings.InstanceName != nil || got.Settings.RetentionMaxMessages != nil {
		t.Fatalf("expected nil overrides initially, got %+v", got.Settings)
	}
	if got.Effective.InstanceName != "Mailcubby" || got.Effective.RetentionMaxMessages != 5000 || got.Effective.RetentionMaxAge != 30*86400 {
		t.Fatalf("unexpected effective defaults: %+v", got.Effective)
	}
	if got.Source.InstanceName != "env" || got.Source.RetentionMaxAge != "env" || got.Source.RetentionMaxMessages != "env" || got.Source.RetentionMaxBytes != "env" {
		t.Fatalf("expected all sources=env, got %+v", got.Source)
	}

	// 2. Validation rejects negative values and empty instance_name.
	rec = doReq(t, h, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"instance_name":          "   ",
		"retention_max_age":      -1,
		"retention_max_messages": -5,
		"retention_max_bytes":    -10,
	}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("PATCH invalid settings status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}

	// 3. Override instance_name and retention defaults.
	rec = doReq(t, h, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"instance_name":          "Agency Mail",
		"retention_max_age":      7 * 86400,
		"retention_max_messages": 250,
		"retention_max_bytes":    0,
	}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /admin/settings status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal updated settings: %v", err)
	}
	if got.Effective.InstanceName != "Agency Mail" || got.Effective.RetentionMaxAge != 7*86400 || got.Effective.RetentionMaxMessages != 250 || got.Effective.RetentionMaxBytes != 0 {
		t.Fatalf("unexpected overridden effective settings: %+v", got.Effective)
	}
	if got.Source.InstanceName != "override" || got.Source.RetentionMaxAge != "override" || got.Source.RetentionMaxMessages != "override" || got.Source.RetentionMaxBytes != "override" {
		t.Fatalf("expected all sources=override, got %+v", got.Source)
	}

	// Verify /meta reflects the updated instance_name.
	rec = doReq(t, h, http.MethodGet, "/api/v1/meta", nil, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /meta status = %d, want 200", rec.Code)
	}
	var meta struct {
		InstanceName string `json:"instance_name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil || meta.InstanceName != "Agency Mail" {
		t.Fatalf("GET /meta instance_name = %q (err=%v), want Agency Mail", meta.InstanceName, err)
	}

	// Verify owner project detail returns updated retention_defaults.
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects", map[string]any{"name": "Demo"}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /projects status = %d, want 201", rec.Code)
	}
	var projDet struct {
		Slug              string `json:"slug"`
		RetentionDefaults *struct {
			RetentionMaxAge      int64 `json:"retention_max_age"`
			RetentionMaxMessages int64 `json:"retention_max_messages"`
			RetentionMaxBytes    int64 `json:"retention_max_bytes"`
		} `json:"retention_defaults"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &projDet); err != nil {
		t.Fatalf("unmarshal project detail: %v", err)
	}
	if projDet.RetentionDefaults == nil || projDet.RetentionDefaults.RetentionMaxMessages != 250 || projDet.RetentionDefaults.RetentionMaxAge != 7*86400 || projDet.RetentionDefaults.RetentionMaxBytes != 0 {
		t.Fatalf("unexpected project retention_defaults: %+v", projDet.RetentionDefaults)
	}

	// 4. Clear retention_max_messages override with null -> falls back to env (5000).
	rec = doReq(t, h, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"retention_max_messages": nil,
	}, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH clear override status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal cleared settings: %v", err)
	}
	if got.Settings.RetentionMaxMessages != nil || got.Effective.RetentionMaxMessages != 5000 || got.Source.RetentionMaxMessages != "env" {
		t.Fatalf("expected retention_max_messages reset to env default, got %+v", got)
	}

	// Verify settings.update audit entries were written (FR-105).
	entries, err := env.store.ListAuditEntries(t.Context(), 50)
	if err != nil {
		t.Fatalf("ListAuditEntries: %v", err)
	}
	var settingsAudits int
	for _, e := range entries {
		if e.Action == "settings.update" {
			settingsAudits++
		}
	}
	if settingsAudits != 2 {
		t.Fatalf("settings.update audit count = %d, want 2", settingsAudits)
	}

	// 5. GET /api/v1/admin/stats returns totals, sizes, ingest rate, version, and uptime (FR-103).
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/"+projDet.Slug+"/send-test", nil, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("send-test status = %d, want 201", rec.Code)
	}
	env.clk.Advance(125 * time.Second)

	rec = doReq(t, h, http.MethodGet, "/api/v1/admin/stats", nil, adminCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/stats status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var stats struct {
		Projects         int64   `json:"projects"`
		Users            int64   `json:"users"`
		Messages         int64   `json:"messages"`
		StoredBytes      int64   `json:"stored_bytes"`
		DBSizeBytes      int64   `json:"db_size_bytes"`
		BlobSizeBytes    int64   `json:"blob_size_bytes"`
		IngestLastHour   int64   `json:"ingest_last_hour"`
		IngestRatePerMin float64 `json:"ingest_rate_per_minute"`
		InstanceMaxBytes int64   `json:"instance_max_bytes"`
		Version          string  `json:"version"`
		UptimeSeconds    int64   `json:"uptime_seconds"`
		StartedAt        string  `json:"started_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("unmarshal admin stats: %v", err)
	}
	if stats.Projects != 1 || stats.Users != 1 || stats.Messages != 1 {
		t.Fatalf("unexpected stats counts: %+v", stats)
	}
	if stats.StoredBytes <= 0 || stats.DBSizeBytes <= 0 || stats.BlobSizeBytes <= 0 {
		t.Fatalf("expected positive storage sizes, got %+v", stats)
	}
	if stats.IngestLastHour != 1 || stats.IngestRatePerMin <= 0 {
		t.Fatalf("unexpected ingest stats: %+v", stats)
	}
	if stats.Version != "test" || stats.UptimeSeconds != 125 || stats.StartedAt == "" {
		t.Fatalf("unexpected runtime stats: %+v", stats)
	}
}
