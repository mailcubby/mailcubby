package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/store"
)

func extractInviteToken(t *testing.T, inviteURL string) string {
	t.Helper()
	const prefix = testBaseURL + "/invite/"
	if !strings.HasPrefix(inviteURL, prefix) {
		t.Fatalf("invite URL %q does not start with %q", inviteURL, prefix)
	}
	tok := strings.TrimPrefix(inviteURL, prefix)
	if tok == "" {
		t.Fatalf("empty token in invite URL %q", inviteURL)
	}
	return tok
}

// FR-050, FR-051: project members CRUD and last-owner protection (409).
func TestProjectMembersAndLastOwnerProtection(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	owner := env.createUser(t, "owner@example.test", "Owner One", pass, false, true, false)
	member := env.createUser(t, "member@example.test", "Member One", pass, false, false, false)
	viewer := env.createUser(t, "viewer@example.test", "Viewer One", pass, false, false, false)

	proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "shop", Name: "Shop", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
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
	memberCookie, memberCSRF := loginAndGetCSRF(t, h, member.Email, pass)
	viewerCookie, _ := loginAndGetCSRF(t, h, viewer.Email, pass)

	// Viewer cannot list members (403); member can list members (200).
	rec := doReq(t, h, http.MethodGet, "/api/v1/projects/shop/members", nil, viewerCookie, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer GET /members status = %d, want 403", rec.Code)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/shop/members", nil, memberCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("member GET /members status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var list []struct {
		User struct {
			ID    int64  `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal members: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len(members) = %d, want 3", len(list))
	}

	// Member cannot update or delete members (403).
	rec = doReq(t, h, http.MethodPatch, "/api/v1/projects/shop/members/"+strconv.FormatInt(viewer.ID, 10),
		map[string]any{"role": "member"}, memberCookie, csrfHeaders(memberCSRF))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member PATCH /members status = %d, want 403", rec.Code)
	}

	// Last owner cannot be demoted (409) or deleted (409).
	ownerPath := "/api/v1/projects/shop/members/" + strconv.FormatInt(owner.ID, 10)
	rec = doReq(t, h, http.MethodPatch, ownerPath, map[string]any{"role": "member"}, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusConflict {
		t.Fatalf("demote last owner status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodDelete, ownerPath, nil, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete last owner status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	// Promote member -> owner, then demote original owner -> member succeeds.
	memberPath := "/api/v1/projects/shop/members/" + strconv.FormatInt(member.ID, 10)
	rec = doReq(t, h, http.MethodPatch, memberPath, map[string]any{"role": "owner"}, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("promote member to owner status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPatch, ownerPath, map[string]any{"role": "member"}, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("demote first owner with second owner present status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// New owner removes viewer.
	viewerPath := "/api/v1/projects/shop/members/" + strconv.FormatInt(viewer.ID, 10)
	rec = doReq(t, h, http.MethodDelete, viewerPath, nil, memberCookie, csrfHeaders(memberCSRF))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete viewer status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/shop", nil, viewerCookie, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("removed viewer GET /projects/shop status = %d, want 404", rec.Code)
	}
}

// FR-055, FR-056, FR-057: project invites create/replace/revoke/expire/preview and all accept flows.
func TestInvitesLifecycleAndAcceptCases(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	owner := env.createUser(t, "owner@example.test", "Owner", pass, false, true, false)
	existing := env.createUser(t, "existing@example.test", "Existing User", pass, false, false, false)
	passwordless := env.createUser(t, "invited@example.test", "Pre Created", "", false, false, false)

	proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug: "portal", Name: "Client Portal", Color: "teal", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := env.store.AddProjectMember(t.Context(), proj.ID, owner.ID, "owner", env.clk.Now().UTC()); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	ownerCookie, ownerCSRF := loginAndGetCSRF(t, h, owner.Email, pass)

	createInvite := func(email, role string) (int64, string) {
		t.Helper()
		rec := doReq(t, h, http.MethodPost, "/api/v1/projects/portal/invites", map[string]any{
			"email": email,
			"role":  role,
		}, ownerCookie, csrfHeaders(ownerCSRF))
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /invites(%s,%s) status = %d, want 201; body=%s", email, role, rec.Code, rec.Body.String())
		}
		var out struct {
			Invite struct {
				ID     int64  `json:"id"`
				Status string `json:"status"`
			} `json:"invite"`
			URL string `json:"url"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal create invite: %v", err)
		}
		if out.Invite.Status != "pending" {
			t.Fatalf("new invite status = %q, want pending", out.Invite.Status)
		}
		return out.Invite.ID, extractInviteToken(t, out.URL)
	}

	// FR-055: creating a second invite for the same email revokes the first.
	_, tok1 := createInvite("newbie@example.test", "viewer")
	_, tok2 := createInvite("newbie@example.test", "member")

	rec := doReq(t, h, http.MethodGet, "/api/v1/invites/"+tok1, nil, nil, nil)
	if rec.Code != http.StatusGone {
		t.Fatalf("superseded invite preview status = %d, want 410", rec.Code)
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/invites/"+tok2, nil, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("active invite preview status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Case 1: unauthenticated new user accepts invite -> created, signed in, added as member.
	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+tok2+"/accept", map[string]any{
		"name":     "Newbie",
		"password": pass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusOK {
		t.Fatalf("new user accept status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	newbieCookie := findSessionCookie(rec)
	if newbieCookie == nil {
		t.Fatalf("expected session cookie after accepting invite")
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/projects/portal", nil, newbieCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("newbie GET /projects/portal status = %d, want 200", rec.Code)
	}
	// Re-using accepted invite returns 410.
	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+tok2+"/accept", map[string]any{
		"name":     "Newbie 2",
		"password": pass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusGone {
		t.Fatalf("reused invite accept status = %d, want 410", rec.Code)
	}

	// Case 2: existing user with password must sign in first (409 login_required),
	// then logged-in accept adds membership; role upgrade works and never downgrades.
	_, existingTok := createInvite(existing.Email, "viewer")
	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+existingTok+"/accept", map[string]any{
		"name":     "Ignored",
		"password": pass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusConflict {
		t.Fatalf("unauthenticated existing user accept status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal login_required error: %v", err)
	}
	if errResp.Error.Code != "login_required" {
		t.Fatalf("error code = %q, want login_required", errResp.Error.Code)
	}

	existingCookie, existingCSRF := loginAndGetCSRF(t, h, existing.Email, pass)
	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+existingTok+"/accept", nil, existingCookie, csrfHeaders(existingCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("logged-in accept status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Upgrade viewer -> owner via invite.
	_, upgradeTok := createInvite(existing.Email, "owner")
	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+upgradeTok+"/accept", nil, existingCookie, csrfHeaders(existingCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("upgrade accept status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	mem, err := env.store.GetProjectMember(t.Context(), proj.ID, existing.ID)
	if err != nil || mem.Role != "owner" {
		t.Fatalf("after upgrade role = %q (err=%v), want owner", mem.Role, err)
	}

	// Accepting a lower-role invite does NOT downgrade owner -> viewer.
	_, downgradeTok := createInvite(existing.Email, "viewer")
	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+downgradeTok+"/accept", nil, existingCookie, csrfHeaders(existingCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("lower-role accept status = %d, want 200", rec.Code)
	}
	mem, err = env.store.GetProjectMember(t.Context(), proj.ID, existing.ID)
	if err != nil || mem.Role != "owner" {
		t.Fatalf("after lower-role invite role = %q (err=%v), want owner (no downgrade)", mem.Role, err)
	}

	// Case 3: instance invite for passwordless user -> logged-in caller gets 409,
	// unauthenticated passwordless user sets password and logs in.
	instRaw, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if _, err := env.store.CreateInvite(t.Context(), store.CreateInviteParams{
		Email:     passwordless.Email,
		TokenHash: auth.HashToken(instRaw),
		CreatedBy: &owner.ID,
		Now:       env.clk.Now().UTC(),
		ExpiresAt: env.clk.Now().UTC().Add(7 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateInvite(instance): %v", err)
	}

	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+instRaw+"/accept", nil, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusConflict {
		t.Fatalf("logged-in instance invite accept status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+instRaw+"/accept", map[string]any{
		"password": pass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusOK {
		t.Fatalf("passwordless user instance accept status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"project"`) {
		t.Fatalf("instance invite accept response should omit project, got %s", rec.Body.String())
	}
	// Verify passwordless user can now log in normally.
	_, _ = loginAndGetCSRF(t, h, passwordless.Email, pass)

	// FR-057: revoke pending invite -> 204 and preview returns 410.
	revokeID, revokeTok := createInvite("revoke@example.test", "viewer")
	rec = doReq(t, h, http.MethodDelete, "/api/v1/projects/portal/invites/"+strconv.FormatInt(revokeID, 10), nil, ownerCookie, csrfHeaders(ownerCSRF))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke invite status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/v1/invites/"+revokeTok, nil, nil, nil)
	if rec.Code != http.StatusGone {
		t.Fatalf("revoked invite preview status = %d, want 410", rec.Code)
	}

	// Expired invite returns 410 after clock advances past InviteTTL.
	_, expTok := createInvite("expire@example.test", "viewer")
	env.clk.Advance(8 * 24 * time.Hour)
	rec = doReq(t, h, http.MethodGet, "/api/v1/invites/"+expTok, nil, nil, nil)
	if rec.Code != http.StatusGone {
		t.Fatalf("expired invite preview status = %d, want 410", rec.Code)
	}
}

// FR-056 AC: two (or more) concurrent accepts of one invite produce exactly one
// membership and 410 Gone for every losing caller.
func TestConcurrentInviteAccept(t *testing.T) {
	t.Parallel()

	t.Run("logged-in concurrent accepts", func(t *testing.T) {
		t.Parallel()
		env := newServerModeEnv(t)
		h := env.srv.Handler()
		const pass = "correct-horse-battery-staple"

		owner := env.createUser(t, "owner@example.test", "Owner", pass, false, true, false)
		proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
			Slug: "race-logged", Name: "Race Logged", Color: "blue", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		if _, err := env.store.AddProjectMember(t.Context(), proj.ID, owner.ID, "owner", env.clk.Now().UTC()); err != nil {
			t.Fatalf("AddProjectMember: %v", err)
		}

		ownerCookie, ownerCSRF := loginAndGetCSRF(t, h, owner.Email, pass)
		rec := doReq(t, h, http.MethodPost, "/api/v1/projects/race-logged/invites", map[string]any{
			"email": "anyone@example.test",
			"role":  "member",
		}, ownerCookie, csrfHeaders(ownerCSRF))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create invite status = %d, want 201", rec.Code)
		}
		var created struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &created)
		tok := extractInviteToken(t, created.URL)

		const contenders = 4
		cookies := make([]*http.Cookie, contenders)
		csrfs := make([]string, contenders)
		for i := range contenders {
			u := env.createUser(t, "contender-"+strconv.Itoa(i)+"@example.test", "Contender "+strconv.Itoa(i), pass, false, false, false)
			cookies[i], csrfs[i] = loginAndGetCSRF(t, h, u.Email, pass)
		}

		start := make(chan struct{})
		codes := make([]int, contenders)
		var wg sync.WaitGroup
		for i := range contenders {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start
				r := doReq(t, h, http.MethodPost, "/api/v1/invites/"+tok+"/accept", nil, cookies[idx], csrfHeaders(csrfs[idx]))
				codes[idx] = r.Code
			}(i)
		}
		close(start)
		wg.Wait()

		okCount, goneCount := 0, 0
		for _, c := range codes {
			switch c {
			case http.StatusOK:
				okCount++
			case http.StatusGone:
				goneCount++
			default:
				t.Errorf("unexpected status %d in concurrent accept (all=%v)", c, codes)
			}
		}
		if okCount != 1 || goneCount != contenders-1 {
			t.Fatalf("concurrent accept statuses = %v, want 1x200 and %dx410", codes, contenders-1)
		}

		members, err := env.store.ListProjectMembers(t.Context(), proj.ID)
		if err != nil {
			t.Fatalf("ListProjectMembers: %v", err)
		}
		// 1 original owner + exactly 1 accepted invite member.
		if len(members) != 2 {
			t.Fatalf("member count = %d, want 2 (1 owner + 1 invite winner)", len(members))
		}
	})

	t.Run("unauthenticated new-user concurrent accepts", func(t *testing.T) {
		t.Parallel()
		env := newServerModeEnv(t)
		h := env.srv.Handler()
		const pass = "correct-horse-battery-staple"

		owner := env.createUser(t, "owner@example.test", "Owner", pass, false, true, false)
		proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
			Slug: "race-anon", Name: "Race Anon", Color: "green", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		if _, err := env.store.AddProjectMember(t.Context(), proj.ID, owner.ID, "owner", env.clk.Now().UTC()); err != nil {
			t.Fatalf("AddProjectMember: %v", err)
		}

		ownerCookie, ownerCSRF := loginAndGetCSRF(t, h, owner.Email, pass)
		rec := doReq(t, h, http.MethodPost, "/api/v1/projects/race-anon/invites", map[string]any{
			"email": "racer@example.test",
			"role":  "viewer",
		}, ownerCookie, csrfHeaders(ownerCSRF))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create invite status = %d, want 201", rec.Code)
		}
		var created struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &created)
		tok := extractInviteToken(t, created.URL)

		const contenders = 2
		start := make(chan struct{})
		codes := make([]int, contenders)
		var wg sync.WaitGroup
		for i := range contenders {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start
				r := doReq(t, h, http.MethodPost, "/api/v1/invites/"+tok+"/accept", map[string]any{
					"name":     "Racer",
					"password": pass,
				}, nil, map[string]string{"Origin": testBaseURL})
				codes[idx] = r.Code
			}(i)
		}
		close(start)
		wg.Wait()

		okCount, goneCount := 0, 0
		for _, c := range codes {
			switch c {
			case http.StatusOK:
				okCount++
			case http.StatusGone:
				goneCount++
			default:
				t.Errorf("unexpected status %d in concurrent unauthenticated accept (all=%v)", c, codes)
			}
		}
		if okCount != 1 || goneCount != contenders-1 {
			t.Fatalf("concurrent unauthenticated accept statuses = %v, want 1x200 and %dx410", codes, contenders-1)
		}

		members, err := env.store.ListProjectMembers(t.Context(), proj.ID)
		if err != nil {
			t.Fatalf("ListProjectMembers: %v", err)
		}
		if len(members) != 2 {
			t.Fatalf("member count = %d, want 2", len(members))
		}
	})
}

// FR-056: an anonymous accept of a PROJECT invite must never set the password of
// an existing passwordless (admin-created) user; only the instance invite may.
// Otherwise any project owner could invite a pending account (even a pending
// instance admin) to their own project and take it over.
func TestProjectInviteCannotSetPasswordOfPasswordlessUser(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"
	const attackerPass = "attacker-chosen-password"
	const victimEmail = "pending-admin@example.test"

	env.createUser(t, "root@example.test", "Root", pass, true, true, false)
	env.createUser(t, "mallory@example.test", "Mallory", pass, false, true, false)

	// Admin onboards a new co-admin: passwordless user plus instance invite.
	rootCookie, rootCSRF := loginAndGetCSRF(t, h, "root@example.test", pass)
	rec := doReq(t, h, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": victimEmail, "name": "Pending Admin", "is_admin": true,
	}, rootCookie, csrfHeaders(rootCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /admin/users status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		InviteURL string `json:"invite_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create user: %v", err)
	}
	instanceTok := extractInviteToken(t, created.InviteURL)

	// A project creator invites the pending account to their own project.
	malCookie, malCSRF := loginAndGetCSRF(t, h, "mallory@example.test", pass)
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects", map[string]any{"name": "Evil", "slug": "evil"}, malCookie, csrfHeaders(malCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /projects status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/evil/invites", map[string]any{
		"email": victimEmail, "role": "viewer",
	}, malCookie, csrfHeaders(malCSRF))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /projects/evil/invites status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var projInv struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &projInv); err != nil {
		t.Fatalf("unmarshal create invite: %v", err)
	}
	projectTok := extractInviteToken(t, projInv.URL)

	// Anonymous accept of the project invite is refused with 409 login_required.
	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+projectTok+"/accept", map[string]any{
		"name": "Owned", "password": attackerPass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusConflict {
		t.Fatalf("anonymous project invite accept status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error.Code != "login_required" {
		t.Fatalf("error code = %q, want login_required", errResp.Error.Code)
	}
	if findSessionCookie(rec) != nil {
		t.Fatalf("refused accept must not set a session cookie")
	}

	victim, err := env.store.GetUserByEmail(t.Context(), victimEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if victim.PasswordHash != nil {
		t.Fatalf("victim password hash was set by a project invite")
	}
	if victim.Name != "Pending Admin" {
		t.Fatalf("victim name = %q, want unchanged", victim.Name)
	}
	proj, err := env.store.GetProjectBySlug(t.Context(), "evil")
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	if _, err := env.store.GetProjectMember(t.Context(), proj.ID, victim.ID); err == nil {
		t.Fatalf("victim was added to the project by a refused accept")
	}

	rec = doReq(t, h, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"email": victimEmail, "password": attackerPass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login with attacker password status = %d, want 401", rec.Code)
	}

	// The refused accept consumed nothing: the project invite is still pending.
	rec = doReq(t, h, http.MethodGet, "/api/v1/invites/"+projectTok, nil, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("project invite preview after refused accept status = %d, want 200", rec.Code)
	}

	// The legitimate instance invite still sets the password.
	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+instanceTok+"/accept", map[string]any{
		"password": pass,
	}, nil, map[string]string{"Origin": testBaseURL})
	if rec.Code != http.StatusOK {
		t.Fatalf("instance invite accept status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	victimCookie, victimCSRF := loginAndGetCSRF(t, h, victimEmail, pass)

	// Once signed in, the project invite is accepted as a logged-in user.
	rec = doReq(t, h, http.MethodPost, "/api/v1/invites/"+projectTok+"/accept", nil, victimCookie, csrfHeaders(victimCSRF))
	if rec.Code != http.StatusOK {
		t.Fatalf("logged-in project invite accept status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
