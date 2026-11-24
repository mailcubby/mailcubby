package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mailcubby/mailcubby/internal/store"
)

func csrfHeaders(csrf string) map[string]string {
	return map[string]string{
		"Origin":       testBaseURL,
		"X-CSRF-Token": csrf,
	}
}

// FR-031, FR-050, FR-051: project creation ownership, settings visibility by role,
// leaving projects, last-owner protection, and admin implicit access audit.
func TestServerModeProjectCreateLeaveAndAdminAudit(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	admin := env.createUser(t, "admin@example.test", "Admin", pass, true, true, false)
	creator := env.createUser(t, "creator@example.test", "Creator", pass, false, true, false)
	basic := env.createUser(t, "basic@example.test", "Basic", pass, false, false, false)

	basicCookie, basicCSRF := loginAndGetCSRF(t, h, basic.Email, pass)
	creatorCookie, creatorCSRF := loginAndGetCSRF(t, h, creator.Email, pass)
	adminCookie, adminCSRF := loginAndGetCSRF(t, h, admin.Email, pass)

	// FR-031 AC: user without can_create_projects gets 403 on POST /api/v1/projects.
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Denied Project",
	}, basicCookie, csrfHeaders(basicCSRF))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("basic POST /projects status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}

	// User with can_create_projects creates a project and becomes owner.
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects", map[string]any{
		"name": "Creator App",
		"slug": "creator-app",
	}, creatorCookie, csrfHeaders(creatorCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("creator POST /projects status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID       int64          `json:"id"`
		Slug     string         `json:"slug"`
		Role     string         `json:"role"`
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created project: %v", err)
	}
	if created.Role != "owner" {
		t.Fatalf("created project role = %q, want owner", created.Role)
	}
	if _, ok := created.Settings["members_can_reveal_smtp"]; !ok {
		t.Fatalf("owner create response missing members_can_reveal_smtp in settings")
	}

	// Non-member basic gets 404 (never 403) on creator-app.
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/creator-app", nil, basicCookie, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-member GET /projects/creator-app status = %d, want 404", rec.Code)
	}

	// Add basic as member; verify GET /projects/{project} hides owner-only settings (SPEC 8.3).
	if _, err := env.store.AddProjectMember(t.Context(), created.ID, basic.ID, "member", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember(basic): %v", err)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/creator-app", nil, basicCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("member GET /projects/creator-app status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var memberProj struct {
		Role     string         `json:"role"`
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &memberProj); err != nil {
		t.Fatalf("unmarshal member project: %v", err)
	}
	if memberProj.Role != "member" {
		t.Fatalf("member project role = %q, want member", memberProj.Role)
	}
	if _, ok := memberProj.Settings["viewer_hide_technical"]; !ok {
		t.Fatalf("member project settings missing viewer_hide_technical")
	}
	if _, ok := memberProj.Settings["plus_address_tags"]; !ok {
		t.Fatalf("member project settings missing plus_address_tags")
	}
	if _, ok := memberProj.Settings["members_can_reveal_smtp"]; ok {
		t.Fatalf("member project settings should not expose members_can_reveal_smtp")
	}
	if _, ok := memberProj.Settings["retention_max_age"]; ok {
		t.Fatalf("member project settings should not expose retention_max_age")
	}

	// FR-051: last owner cannot leave (409).
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/creator-app/leave", nil, creatorCookie, csrfHeaders(creatorCSRF))
	if rec.Code != http.StatusConflict {
		t.Fatalf("last owner leave status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	// Non-owner member can leave (204), then loses access (404).
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/creator-app/leave", nil, basicCookie, csrfHeaders(basicCSRF))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("member leave status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/creator-app", nil, basicCookie, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("after leave GET /projects/creator-app status = %d, want 404", rec.Code)
	}

	// Admin implicit access to creator-app succeeds and writes project.admin_access once per session.
	for i := range 2 {
		rec = doReq(t, h, http.MethodGet, "/api/v1/projects/creator-app", nil, adminCookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("admin access #%d status = %d, want 200; body=%s", i+1, rec.Code, rec.Body.String())
		}
	}
	countAdminAccess := func(projectID int64) int {
		entries, err := env.store.ListAuditEntries(t.Context(), 100)
		if err != nil {
			t.Fatalf("ListAuditEntries: %v", err)
		}
		n := 0
		for _, e := range entries {
			if e.Action == "project.admin_access" && e.ProjectID != nil && *e.ProjectID == projectID {
				n++
			}
		}
		return n
	}
	if got := countAdminAccess(created.ID); got != 1 {
		t.Fatalf("project.admin_access count within one session = %d, want 1", got)
	}

	// Admin who is not an explicit member cannot leave (409).
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/creator-app/leave", nil, adminCookie, csrfHeaders(adminCSRF))
	if rec.Code != http.StatusConflict {
		t.Fatalf("implicit admin leave status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	// A new admin session accessing the same project writes a second project.admin_access entry.
	adminCookie2, _ := loginAndGetCSRF(t, h, admin.Email, pass)
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/creator-app", nil, adminCookie2, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin session #2 access status = %d, want 200", rec.Code)
	}
	if got := countAdminAccess(created.ID); got != 2 {
		t.Fatalf("project.admin_access count across two sessions = %d, want 2", got)
	}

	// When admin IS an explicit member of a project, no project.admin_access is written.
	adminProj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug:     "admin-owned",
		Name:     "Admin Owned",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject(admin-owned): %v", err)
	}
	if _, err := env.store.AddProjectMember(t.Context(), adminProj.ID, admin.ID, "owner", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember(admin): %v", err)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/admin-owned", nil, adminCookie2, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin explicit member GET status = %d, want 200", rec.Code)
	}
	if got := countAdminAccess(adminProj.ID); got != 0 {
		t.Fatalf("explicit member admin wrote %d project.admin_access entries, want 0", got)
	}

	// Adding a second owner allows the first owner to leave.
	if _, err := env.store.AddProjectMember(t.Context(), created.ID, basic.ID, "owner", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember(basic as owner): %v", err)
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/creator-app/leave", nil, creatorCookie, csrfHeaders(creatorCSRF))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("one of two owners leave status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}
