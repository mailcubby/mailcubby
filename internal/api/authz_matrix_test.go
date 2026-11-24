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

	"github.com/mailcubby/mailcubby/internal/api"
	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/blob"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/store"
	"github.com/mailcubby/mailcubby/internal/testutil"
)

type authzRole int

const (
	roleNone authzRole = iota
	roleOutsider
	roleViewer
	roleMember
	roleOwner
	roleAdmin
	roleTokenRead
	roleTokenWrite
)

type roleCreds struct {
	user   store.User
	cookie *http.Cookie
	csrf   string
	bearer string
}

type routeAuthzSpec struct {
	// expected maps each principal role to its expected HTTP status code.
	expected map[authzRole]int
	// viewerTechOff overrides expected[roleViewer] when viewer_hide_technical=false.
	viewerTechOff int
	// memberRevealOn overrides expected[roleMember] when members_can_reveal_smtp=true.
	memberRevealOn int
	// makeReq builds a request (path + optional JSON body) for testing this route
	// with the given principal. Fresh disposable resources are created when the
	// principal is expected to succeed on a mutating/destructive route.
	makeReq func(t *testing.T, role authzRole, creds roleCreds) (path string, body any, customCookie *http.Cookie, customCSRF string)
}

// FR-050: authorization matrix test iterating every registered API route across
// none, outsider, viewer (hide_tech on & off), member, owner, and admin.
// Adding a route without an entry in this matrix fails the test.
func TestAuthorizationMatrix(t *testing.T) {
	t.Parallel()

	env := newServerModeEnv(t)
	h := env.srv.Handler()
	const pass = "correct-horse-battery-staple"

	adminUser := env.createUser(t, "admin@example.test", "Admin", pass, true, true, false)
	ownerUser := env.createUser(t, "owner@example.test", "Owner", pass, false, true, false)
	memberUser := env.createUser(t, "member@example.test", "Member", pass, false, false, false)
	viewerUser := env.createUser(t, "viewer@example.test", "Viewer", pass, false, false, false)
	outsiderUser := env.createUser(t, "outsider@example.test", "Outsider", pass, false, false, false)

	// Main fixture project with default settings (viewer_hide_technical=true).
	proj, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug:     "matrix-proj",
		Name:     "Matrix Project",
		Color:    "blue",
		Settings: store.DefaultProjectSettings(),
		Now:      env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	// Second project with viewer_hide_technical=false and members_can_reveal_smtp=true to test dynamic setting unlocks.
	techOffSettings := store.DefaultProjectSettings()
	techOffSettings.ViewerHideTechnical = false
	techOffSettings.MembersCanRevealSMTP = true
	projTechOff, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
		Slug:     "matrix-tech-off",
		Name:     "Matrix Tech Off",
		Color:    "green",
		Settings: techOffSettings,
		Now:      env.clk.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateProject(tech-off): %v", err)
	}

	for _, p := range []store.Project{proj, projTechOff} {
		for _, m := range []struct {
			uid  int64
			role string
		}{
			{ownerUser.ID, "owner"},
			{memberUser.ID, "member"},
			{viewerUser.ID, "viewer"},
		} {
			if _, err := env.store.AddProjectMember(t.Context(), p.ID, m.uid, m.role, env.clk.Now().UTC()); err != nil {
				t.Fatalf("AddProjectMember: %v", err)
			}
		}
	}

	credsByRole := map[authzRole]roleCreds{
		roleNone: {},
	}
	for r, u := range map[authzRole]store.User{
		roleOutsider: outsiderUser,
		roleViewer:   viewerUser,
		roleMember:   memberUser,
		roleOwner:    ownerUser,
		roleAdmin:    adminUser,
	} {
		c, csrf := loginAndGetCSRF(t, h, u.Email, pass)
		credsByRole[r] = roleCreds{user: u, cookie: c, csrf: csrf}
	}

	// Create personal API tokens (read and write) owned by ownerUser for the matrix (FR-050, FR-066).
	createAPIToken := func(scope, name string) string {
		t.Helper()
		oc := credsByRole[roleOwner]
		rec := doReq(t, h, http.MethodPost, "/api/v1/tokens", map[string]any{
			"name": name, "scope": scope,
		}, oc.cookie, csrfHeaders(oc.csrf))
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed api token(%s) status = %d, want 201; body=%s", scope, rec.Code, rec.Body.String())
		}
		var out struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal seed api token: %v", err)
		}
		return out.Token
	}
	readTok := createAPIToken("read", "Matrix Read Token")
	writeTok := createAPIToken("write", "Matrix Write Token")
	credsByRole[roleTokenRead] = roleCreds{user: ownerUser, bearer: readTok}
	credsByRole[roleTokenWrite] = roleCreds{user: ownerUser, bearer: writeTok}

	sendSample := func(slug string) (string, int64) {
		t.Helper()
		oc := credsByRole[roleOwner]
		rec := doReq(t, h, http.MethodPost, "/api/v1/projects/"+slug+"/send-test", nil, oc.cookie, csrfHeaders(oc.csrf))
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed send-test(%s) status = %d, want 201", slug, rec.Code)
		}
		var s struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatalf("unmarshal send-test: %v", err)
		}
		detRec := doReq(t, h, http.MethodGet, "/api/v1/projects/"+slug+"/messages/"+s.ID, nil, oc.cookie, nil)
		var d struct {
			Attachments []struct {
				ID int64 `json:"id"`
			} `json:"attachments"`
		}
		if err := json.Unmarshal(detRec.Body.Bytes(), &d); err != nil || len(d.Attachments) == 0 {
			t.Fatalf("seed attachment lookup failed: err=%v len=%d", err, len(d.Attachments))
		}
		return s.ID, d.Attachments[0].ID
	}

	msgID, attID := sendSample(proj.Slug)
	msgTechOffID, _ := sendSample(projTechOff.Slug)

	seedTag, err := env.store.CreateTag(t.Context(), proj.ID, "seed-tag", "slate", env.clk.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	createSeedCred := func(slug, label string) int64 {
		t.Helper()
		oc := credsByRole[roleOwner]
		rec := doReq(t, h, http.MethodPost, "/api/v1/projects/"+slug+"/smtp-credentials", map[string]any{
			"label": label,
		}, oc.cookie, csrfHeaders(oc.csrf))
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed smtp credential(%s) status = %d, want 201; body=%s", slug, rec.Code, rec.Body.String())
		}
		var out struct {
			Credential struct {
				ID int64 `json:"id"`
			} `json:"credential"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal seed credential: %v", err)
		}
		return out.Credential.ID
	}

	seedCredID := createSeedCred(proj.Slug, "seed-cred")
	seedCredRevealID := createSeedCred(projTechOff.Slug, "seed-cred-reveal")

	seq := 0
	nextSeq := func() int {
		seq++
		return seq
	}

	newDisposableInvite := func(projectID int64) (int64, string) {
		t.Helper()
		raw, err := auth.GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken: %v", err)
		}
		roleStr := "viewer"
		inv, err := env.store.CreateInvite(t.Context(), store.CreateInviteParams{
			ProjectID: &projectID,
			Email:     "disp-" + strconv.Itoa(nextSeq()) + "@example.test",
			Role:      &roleStr,
			TokenHash: auth.HashToken(raw),
			CreatedBy: &ownerUser.ID,
			Now:       env.clk.Now().UTC(),
			ExpiresAt: env.clk.Now().UTC().Add(24 * time.Hour),
		})
		if err != nil {
			t.Fatalf("CreateInvite: %v", err)
		}
		return inv.ID, raw
	}

	newDisposableUser := func() store.User {
		t.Helper()
		return env.createUser(t, "disp-u-"+strconv.Itoa(nextSeq())+"@example.test", "Disposable", pass, false, false, false)
	}

	newDisposableProject := func(withViewer, withMember, withOwner bool) store.Project {
		t.Helper()
		slug := "disp-proj-" + strconv.Itoa(nextSeq())
		p, err := env.store.CreateProject(t.Context(), store.CreateProjectParams{
			Slug: slug, Name: "Disp " + slug, Color: "slate", Settings: store.DefaultProjectSettings(), Now: env.clk.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("CreateProject(%s): %v", slug, err)
		}
		// Always keep a spare owner so non-last-owner actions succeed cleanly.
		spare := newDisposableUser()
		if _, err := env.store.AddProjectMember(t.Context(), p.ID, spare.ID, "owner", env.clk.Now().UTC()); err != nil {
			t.Fatalf("AddProjectMember(spare): %v", err)
		}
		if withOwner {
			if _, err := env.store.AddProjectMember(t.Context(), p.ID, ownerUser.ID, "owner", env.clk.Now().UTC()); err != nil {
				t.Fatalf("AddProjectMember(owner): %v", err)
			}
		}
		if withMember {
			if _, err := env.store.AddProjectMember(t.Context(), p.ID, memberUser.ID, "member", env.clk.Now().UTC()); err != nil {
				t.Fatalf("AddProjectMember(member): %v", err)
			}
		}
		if withViewer {
			if _, err := env.store.AddProjectMember(t.Context(), p.ID, viewerUser.ID, "viewer", env.clk.Now().UTC()); err != nil {
				t.Fatalf("AddProjectMember(viewer): %v", err)
			}
		}
		return p
	}

	constPath := func(p string, body any) func(*testing.T, authzRole, roleCreds) (string, any, *http.Cookie, string) {
		return func(*testing.T, authzRole, roleCreds) (string, any, *http.Cookie, string) {
			return p, body, nil, ""
		}
	}

	publicOK := map[authzRole]int{
		roleNone: 200, roleOutsider: 200, roleViewer: 200, roleMember: 200, roleOwner: 200, roleAdmin: 200, roleTokenRead: 200, roleTokenWrite: 200,
	}
	userOK := map[authzRole]int{
		roleNone: 401, roleOutsider: 200, roleViewer: 200, roleMember: 200, roleOwner: 200, roleAdmin: 200, roleTokenRead: 200, roleTokenWrite: 200,
	}
	sessionOK := map[authzRole]int{
		roleNone: 401, roleOutsider: 200, roleViewer: 200, roleMember: 200, roleOwner: 200, roleAdmin: 200, roleTokenRead: 403, roleTokenWrite: 403,
	}
	session201 := map[authzRole]int{
		roleNone: 401, roleOutsider: 201, roleViewer: 201, roleMember: 201, roleOwner: 201, roleAdmin: 201, roleTokenRead: 403, roleTokenWrite: 403,
	}
	session204 := map[authzRole]int{
		roleNone: 401, roleOutsider: 204, roleViewer: 204, roleMember: 204, roleOwner: 204, roleAdmin: 204, roleTokenRead: 403, roleTokenWrite: 403,
	}
	viewerOK := map[authzRole]int{
		roleNone: 401, roleOutsider: 404, roleViewer: 200, roleMember: 200, roleOwner: 200, roleAdmin: 200, roleTokenRead: 200, roleTokenWrite: 200,
	}
	// FR-066: viewer_hide_technical applies only when the user's own role is viewer; owner's read/write tokens can read technical views.
	viewerTechOK := map[authzRole]int{
		roleNone: 401, roleOutsider: 404, roleViewer: 403, roleMember: 200, roleOwner: 200, roleAdmin: 200, roleTokenRead: 200, roleTokenWrite: 200,
	}
	memberOK := map[authzRole]int{
		roleNone: 401, roleOutsider: 404, roleViewer: 403, roleMember: 200, roleOwner: 200, roleAdmin: 200, roleTokenRead: 403, roleTokenWrite: 200,
	}
	member201 := map[authzRole]int{
		roleNone: 401, roleOutsider: 404, roleViewer: 403, roleMember: 201, roleOwner: 201, roleAdmin: 201, roleTokenRead: 403, roleTokenWrite: 201,
	}
	member204 := map[authzRole]int{
		roleNone: 401, roleOutsider: 404, roleViewer: 403, roleMember: 204, roleOwner: 204, roleAdmin: 204, roleTokenRead: 403, roleTokenWrite: 204,
	}
	memberNoTokenOK := map[authzRole]int{
		roleNone: 401, roleOutsider: 404, roleViewer: 403, roleMember: 200, roleOwner: 200, roleAdmin: 200, roleTokenRead: 403, roleTokenWrite: 403,
	}
	ownerOK := map[authzRole]int{
		roleNone: 401, roleOutsider: 404, roleViewer: 403, roleMember: 403, roleOwner: 200, roleAdmin: 200, roleTokenRead: 403, roleTokenWrite: 403,
	}
	owner201 := map[authzRole]int{
		roleNone: 401, roleOutsider: 404, roleViewer: 403, roleMember: 403, roleOwner: 201, roleAdmin: 201, roleTokenRead: 403, roleTokenWrite: 403,
	}
	owner204 := map[authzRole]int{
		roleNone: 401, roleOutsider: 404, roleViewer: 403, roleMember: 403, roleOwner: 204, roleAdmin: 204, roleTokenRead: 403, roleTokenWrite: 403,
	}
	adminOK := map[authzRole]int{
		roleNone: 401, roleOutsider: 403, roleViewer: 403, roleMember: 403, roleOwner: 403, roleAdmin: 200, roleTokenRead: 403, roleTokenWrite: 403,
	}

	matrix := map[string]routeAuthzSpec{
		// Health & OpenAPI
		"GET /healthz":             {expected: publicOK, makeReq: constPath("/healthz", nil)},
		"GET /readyz":              {expected: publicOK, makeReq: constPath("/readyz", nil)},
		"GET /api/v1/openapi.yaml": {expected: publicOK, makeReq: constPath("/api/v1/openapi.yaml", nil)},

		// Meta, Setup & Auth
		"GET /api/v1/meta": {expected: publicOK, makeReq: constPath("/api/v1/meta", nil)},
		"POST /api/v1/setup": {
			// Setup is already complete in this fixture -> 409 for all callers (public route).
			expected: map[authzRole]int{
				roleNone: 409, roleOutsider: 409, roleViewer: 409, roleMember: 409, roleOwner: 409, roleAdmin: 409, roleTokenRead: 409, roleTokenWrite: 409,
			},
			makeReq: constPath("/api/v1/setup", map[string]any{
				"setup_token": "tok", "email": "x@example.test", "name": "X", "password": pass,
			}),
		},
		"POST /api/v1/auth/login": {
			expected: publicOK,
			makeReq: constPath("/api/v1/auth/login", map[string]any{
				"email": outsiderUser.Email, "password": pass,
			}),
		},
		"POST /api/v1/auth/logout": {
			expected: session204,
			makeReq: func(t *testing.T, role authzRole, creds roleCreds) (string, any, *http.Cookie, string) {
				if role == roleNone || role == roleTokenRead || role == roleTokenWrite {
					return "/api/v1/auth/logout", nil, nil, ""
				}
				// Use a fresh disposable session so the shared test session stays active.
				c, csrf := loginAndGetCSRF(t, h, creds.user.Email, pass)
				return "/api/v1/auth/logout", nil, c, csrf
			},
		},
		"GET /api/v1/auth/me": {expected: userOK, makeReq: constPath("/api/v1/auth/me", nil)},
		"PATCH /api/v1/auth/me": {
			expected: sessionOK,
			makeReq: func(_ *testing.T, _ authzRole, creds roleCreds) (string, any, *http.Cookie, string) {
				name := creds.user.Name
				if name == "" {
					name = "Anon"
				}
				return "/api/v1/auth/me", map[string]any{"name": name}, nil, ""
			},
		},
		"GET /api/v1/auth/sessions": {expected: sessionOK, makeReq: constPath("/api/v1/auth/sessions", nil)},
		"DELETE /api/v1/auth/sessions/{id}": {
			expected: session204,
			makeReq: func(t *testing.T, role authzRole, creds roleCreds) (string, any, *http.Cookie, string) {
				if role == roleNone || role == roleTokenRead || role == roleTokenWrite {
					return "/api/v1/auth/sessions/nonexistent", nil, nil, ""
				}
				c, _ := loginAndGetCSRF(t, h, creds.user.Email, pass)
				return "/api/v1/auth/sessions/" + auth.HashToken(c.Value), nil, nil, ""
			},
		},
		"GET /api/v1/invites/{token}": {
			expected: publicOK,
			makeReq: func(t *testing.T, _ authzRole, _ roleCreds) (string, any, *http.Cookie, string) {
				_, tok := newDisposableInvite(proj.ID)
				return "/api/v1/invites/" + tok, nil, nil, ""
			},
		},
		"POST /api/v1/invites/{token}/accept": {
			expected: map[authzRole]int{
				roleNone: 200, roleOutsider: 200, roleViewer: 200, roleMember: 200, roleOwner: 200, roleAdmin: 200, roleTokenRead: 403, roleTokenWrite: 403,
			},
			makeReq: func(t *testing.T, role authzRole, _ roleCreds) (string, any, *http.Cookie, string) {
				dp := newDisposableProject(false, false, false)
				_, tok := newDisposableInvite(dp.ID)
				if role == roleNone {
					return "/api/v1/invites/" + tok + "/accept", map[string]any{
						"name": "Invited New", "password": pass,
					}, nil, ""
				}
				return "/api/v1/invites/" + tok + "/accept", nil, nil, ""
			},
		},
		"POST /api/v1/password-reset/{token}": {
			// Public route: an unknown token returns 410 (proving no auth wall).
			expected: map[authzRole]int{
				roleNone: 410, roleOutsider: 410, roleViewer: 410, roleMember: 410, roleOwner: 410, roleAdmin: 410, roleTokenRead: 410, roleTokenWrite: 410,
			},
			makeReq: constPath("/api/v1/password-reset/unknown-token", map[string]any{"password": pass}),
		},

		// Projects
		"GET /api/v1/projects": {expected: userOK, makeReq: constPath("/api/v1/projects", nil)},
		"POST /api/v1/projects": {
			// ownerUser has can_create_projects=true; outsider/viewer/member have can_create_projects=false; tokens cannot create projects.
			expected: map[authzRole]int{
				roleNone: 401, roleOutsider: 403, roleViewer: 403, roleMember: 403, roleOwner: 201, roleAdmin: 201, roleTokenRead: 403, roleTokenWrite: 403,
			},
			makeReq: func(*testing.T, authzRole, roleCreds) (string, any, *http.Cookie, string) {
				n := strconv.Itoa(nextSeq())
				return "/api/v1/projects", map[string]any{"name": "Created " + n, "slug": "created-" + n}, nil, ""
			},
		},
		"GET /api/v1/projects/{project}": {expected: viewerOK, makeReq: constPath("/api/v1/projects/"+proj.Slug, nil)},
		"PATCH /api/v1/projects/{project}": {
			expected: ownerOK,
			makeReq:  constPath("/api/v1/projects/"+proj.Slug, map[string]any{"name": "Matrix Project"}),
		},
		"DELETE /api/v1/projects/{project}": {
			expected: owner204,
			makeReq: func(t *testing.T, role authzRole, _ roleCreds) (string, any, *http.Cookie, string) {
				if role == roleOwner || role == roleAdmin {
					dp := newDisposableProject(true, true, true)
					return "/api/v1/projects/" + dp.Slug, map[string]any{"confirm_slug": dp.Slug}, nil, ""
				}
				return "/api/v1/projects/" + proj.Slug, map[string]any{"confirm_slug": proj.Slug}, nil, ""
			},
		},
		"POST /api/v1/projects/{project}/leave": {
			// Admin is not an explicit member of the disposable project -> 409 (passed auth & middleware!).
			expected: map[authzRole]int{
				roleNone: 401, roleOutsider: 404, roleViewer: 204, roleMember: 204, roleOwner: 204, roleAdmin: 409, roleTokenRead: 403, roleTokenWrite: 403,
			},
			makeReq: func(t *testing.T, _ authzRole, _ roleCreds) (string, any, *http.Cookie, string) {
				dp := newDisposableProject(true, true, true)
				return "/api/v1/projects/" + dp.Slug + "/leave", nil, nil, ""
			},
		},
		"GET /api/v1/projects/{project}/audit": {
			expected: ownerOK,
			makeReq:  constPath("/api/v1/projects/"+proj.Slug+"/audit", nil),
		},

		// Messages
		"GET /api/v1/projects/{project}/messages": {expected: viewerOK, makeReq: constPath("/api/v1/projects/"+proj.Slug+"/messages", nil)},
		"DELETE /api/v1/projects/{project}/messages": {
			expected: memberOK,
			makeReq: func(t *testing.T, role authzRole, _ roleCreds) (string, any, *http.Cookie, string) {
				if role == roleMember || role == roleOwner || role == roleAdmin || role == roleTokenWrite {
					dp := newDisposableProject(true, true, true)
					return "/api/v1/projects/" + dp.Slug + "/messages", nil, nil, ""
				}
				return "/api/v1/projects/" + proj.Slug + "/messages", nil, nil, ""
			},
		},
		"GET /api/v1/projects/{project}/messages/wait": {
			expected: viewerOK,
			makeReq:  constPath("/api/v1/projects/"+proj.Slug+"/messages/wait?since=00000000000000000000000000&timeout=1s", nil),
		},
		"POST /api/v1/projects/{project}/messages/bulk": {
			expected: memberOK,
			makeReq: constPath("/api/v1/projects/"+proj.Slug+"/messages/bulk", map[string]any{
				"ids": []string{msgID}, "action": "unseen",
			}),
		},
		"GET /api/v1/projects/{project}/messages/{id}": {
			expected: viewerOK,
			makeReq:  constPath("/api/v1/projects/"+proj.Slug+"/messages/"+msgID, nil),
		},
		"PATCH /api/v1/projects/{project}/messages/{id}": {
			expected: memberOK,
			makeReq:  constPath("/api/v1/projects/"+proj.Slug+"/messages/"+msgID, map[string]any{"seen": false}),
		},
		"DELETE /api/v1/projects/{project}/messages/{id}": {
			expected: member204,
			makeReq: func(t *testing.T, role authzRole, _ roleCreds) (string, any, *http.Cookie, string) {
				if role == roleMember || role == roleOwner || role == roleAdmin || role == roleTokenWrite {
					id, _ := sendSample(proj.Slug)
					return "/api/v1/projects/" + proj.Slug + "/messages/" + id, nil, nil, ""
				}
				return "/api/v1/projects/" + proj.Slug + "/messages/" + msgID, nil, nil, ""
			},
		},
		"GET /api/v1/projects/{project}/messages/{id}/raw": {
			expected: viewerTechOK, viewerTechOff: 200,
			makeReq: constPath("/api/v1/projects/"+proj.Slug+"/messages/"+msgID+"/raw", nil),
		},
		"GET /api/v1/projects/{project}/messages/{id}/eml": {
			expected: viewerTechOK, viewerTechOff: 200,
			makeReq: constPath("/api/v1/projects/"+proj.Slug+"/messages/"+msgID+"/eml", nil),
		},
		"GET /api/v1/projects/{project}/messages/{id}/html": {
			expected: viewerTechOK, viewerTechOff: 200,
			makeReq: constPath("/api/v1/projects/"+proj.Slug+"/messages/"+msgID+"/html", nil),
		},
		"GET /api/v1/projects/{project}/messages/{id}/headers": {
			expected: viewerTechOK, viewerTechOff: 200,
			makeReq: constPath("/api/v1/projects/"+proj.Slug+"/messages/"+msgID+"/headers", nil),
		},
		"GET /api/v1/projects/{project}/messages/{id}/attachments/{attachment_id}": {
			expected: viewerOK,
			makeReq:  constPath("/api/v1/projects/"+proj.Slug+"/messages/"+msgID+"/attachments/"+strconv.FormatInt(attID, 10), nil),
		},
		"POST /api/v1/projects/{project}/send-test": {
			expected: member201,
			makeReq:  constPath("/api/v1/projects/"+proj.Slug+"/send-test", nil),
		},

		// Tags
		"GET /api/v1/projects/{project}/tags": {expected: viewerOK, makeReq: constPath("/api/v1/projects/"+proj.Slug+"/tags", nil)},
		"POST /api/v1/projects/{project}/tags": {
			expected: member201,
			makeReq: func(*testing.T, authzRole, roleCreds) (string, any, *http.Cookie, string) {
				return "/api/v1/projects/" + proj.Slug + "/tags", map[string]any{"name": "tag-" + strconv.Itoa(nextSeq())}, nil, ""
			},
		},
		"PATCH /api/v1/projects/{project}/tags/{id}": {
			expected: memberOK,
			makeReq:  constPath("/api/v1/projects/"+proj.Slug+"/tags/"+strconv.FormatInt(seedTag.ID, 10), map[string]any{"color": "blue"}),
		},
		"DELETE /api/v1/projects/{project}/tags/{id}": {
			expected: member204,
			makeReq: func(t *testing.T, role authzRole, _ roleCreds) (string, any, *http.Cookie, string) {
				if role == roleMember || role == roleOwner || role == roleAdmin || role == roleTokenWrite {
					tg, err := env.store.CreateTag(t.Context(), proj.ID, "del-tag-"+strconv.Itoa(nextSeq()), "slate", env.clk.Now().UTC())
					if err != nil {
						t.Fatalf("CreateTag: %v", err)
					}
					return "/api/v1/projects/" + proj.Slug + "/tags/" + strconv.FormatInt(tg.ID, 10), nil, nil, ""
				}
				return "/api/v1/projects/" + proj.Slug + "/tags/" + strconv.FormatInt(seedTag.ID, 10), nil, nil, ""
			},
		},

		// Members & Invites
		"GET /api/v1/projects/{project}/members": {expected: memberNoTokenOK, makeReq: constPath("/api/v1/projects/"+proj.Slug+"/members", nil)},
		"PATCH /api/v1/projects/{project}/members/{user_id}": {
			expected: ownerOK,
			makeReq:  constPath("/api/v1/projects/"+proj.Slug+"/members/"+strconv.FormatInt(viewerUser.ID, 10), map[string]any{"role": "viewer"}),
		},
		"DELETE /api/v1/projects/{project}/members/{user_id}": {
			expected: owner204,
			makeReq: func(t *testing.T, role authzRole, _ roleCreds) (string, any, *http.Cookie, string) {
				if role == roleOwner || role == roleAdmin {
					u := newDisposableUser()
					if _, err := env.store.AddProjectMember(t.Context(), proj.ID, u.ID, "viewer", env.clk.Now().UTC()); err != nil {
						t.Fatalf("AddProjectMember: %v", err)
					}
					return "/api/v1/projects/" + proj.Slug + "/members/" + strconv.FormatInt(u.ID, 10), nil, nil, ""
				}
				return "/api/v1/projects/" + proj.Slug + "/members/" + strconv.FormatInt(viewerUser.ID, 10), nil, nil, ""
			},
		},
		"GET /api/v1/projects/{project}/invites": {expected: ownerOK, makeReq: constPath("/api/v1/projects/"+proj.Slug+"/invites", nil)},
		"POST /api/v1/projects/{project}/invites": {
			expected: owner201,
			makeReq: func(*testing.T, authzRole, roleCreds) (string, any, *http.Cookie, string) {
				return "/api/v1/projects/" + proj.Slug + "/invites", map[string]any{
					"email": "inv-" + strconv.Itoa(nextSeq()) + "@example.test", "role": "viewer",
				}, nil, ""
			},
		},
		"DELETE /api/v1/projects/{project}/invites/{id}": {
			expected: owner204,
			makeReq: func(t *testing.T, _ authzRole, _ roleCreds) (string, any, *http.Cookie, string) {
				invID, _ := newDisposableInvite(proj.ID)
				return "/api/v1/projects/" + proj.Slug + "/invites/" + strconv.FormatInt(invID, 10), nil, nil, ""
			},
		},

		// SMTP Credentials
		"GET /api/v1/projects/{project}/smtp-credentials": {
			expected: memberNoTokenOK,
			makeReq:  constPath("/api/v1/projects/"+proj.Slug+"/smtp-credentials", nil),
		},
		"POST /api/v1/projects/{project}/smtp-credentials": {
			expected: owner201,
			makeReq: func(*testing.T, authzRole, roleCreds) (string, any, *http.Cookie, string) {
				return "/api/v1/projects/" + proj.Slug + "/smtp-credentials", map[string]any{
					"label": "cred-" + strconv.Itoa(nextSeq()),
				}, nil, ""
			},
		},
		"POST /api/v1/projects/{project}/smtp-credentials/{id}/reveal": {
			expected:       ownerOK,
			memberRevealOn: 200,
			makeReq:        constPath("/api/v1/projects/"+proj.Slug+"/smtp-credentials/"+strconv.FormatInt(seedCredID, 10)+"/reveal", nil),
		},
		"POST /api/v1/projects/{project}/smtp-credentials/{id}/rotate": {
			expected: ownerOK,
			makeReq:  constPath("/api/v1/projects/"+proj.Slug+"/smtp-credentials/"+strconv.FormatInt(seedCredID, 10)+"/rotate", nil),
		},
		"PATCH /api/v1/projects/{project}/smtp-credentials/{id}": {
			expected: ownerOK,
			makeReq:  constPath("/api/v1/projects/"+proj.Slug+"/smtp-credentials/"+strconv.FormatInt(seedCredID, 10), map[string]any{"label": "seed-cred"}),
		},
		"DELETE /api/v1/projects/{project}/smtp-credentials/{id}": {
			expected: owner204,
			makeReq: func(t *testing.T, role authzRole, _ roleCreds) (string, any, *http.Cookie, string) {
				if role == roleOwner || role == roleAdmin {
					id := createSeedCred(proj.Slug, "del-cred-"+strconv.Itoa(nextSeq()))
					return "/api/v1/projects/" + proj.Slug + "/smtp-credentials/" + strconv.FormatInt(id, 10), nil, nil, ""
				}
				return "/api/v1/projects/" + proj.Slug + "/smtp-credentials/" + strconv.FormatInt(seedCredID, 10), nil, nil, ""
			},
		},

		// API Tokens (SPEC 8.7)
		"GET /api/v1/tokens": {
			expected: sessionOK,
			makeReq:  constPath("/api/v1/tokens", nil),
		},
		"POST /api/v1/tokens": {
			expected: session201,
			makeReq: func(*testing.T, authzRole, roleCreds) (string, any, *http.Cookie, string) {
				return "/api/v1/tokens", map[string]any{
					"name": "tok-" + strconv.Itoa(nextSeq()), "scope": "read",
				}, nil, ""
			},
		},
		"DELETE /api/v1/tokens/{id}": {
			expected: session204,
			makeReq: func(t *testing.T, role authzRole, creds roleCreds) (string, any, *http.Cookie, string) {
				if role == roleNone || role == roleTokenRead || role == roleTokenWrite {
					return "/api/v1/tokens/999999", nil, nil, ""
				}
				rec := doReq(t, h, http.MethodPost, "/api/v1/tokens", map[string]any{
					"name": "del-tok-" + strconv.Itoa(nextSeq()), "scope": "read",
				}, creds.cookie, csrfHeaders(creds.csrf))
				if rec.Code != http.StatusCreated {
					t.Fatalf("seed disposable api token = %d, want 201", rec.Code)
				}
				var out struct {
					TokenInfo struct {
						ID int64 `json:"id"`
					} `json:"token_info"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
					t.Fatalf("unmarshal disposable token: %v", err)
				}
				return "/api/v1/tokens/" + strconv.FormatInt(out.TokenInfo.ID, 10), nil, nil, ""
			},
		},

		// Admin
		"GET /api/v1/admin/users": {expected: adminOK, makeReq: constPath("/api/v1/admin/users", nil)},
		"POST /api/v1/admin/users": {
			expected: map[authzRole]int{
				roleNone: 401, roleOutsider: 403, roleViewer: 403, roleMember: 403, roleOwner: 403, roleAdmin: 201, roleTokenRead: 403, roleTokenWrite: 403,
			},
			makeReq: func(*testing.T, authzRole, roleCreds) (string, any, *http.Cookie, string) {
				return "/api/v1/admin/users", map[string]any{
					"email": "adm-new-" + strconv.Itoa(nextSeq()) + "@example.test", "name": "New User",
				}, nil, ""
			},
		},
		"PATCH /api/v1/admin/users/{id}": {
			expected: adminOK,
			makeReq:  constPath("/api/v1/admin/users/"+strconv.FormatInt(outsiderUser.ID, 10), map[string]any{"name": "Outsider"}),
		},
		"DELETE /api/v1/admin/users/{id}": {
			expected: map[authzRole]int{
				roleNone: 401, roleOutsider: 403, roleViewer: 403, roleMember: 403, roleOwner: 403, roleAdmin: 204, roleTokenRead: 403, roleTokenWrite: 403,
			},
			makeReq: func(t *testing.T, role authzRole, _ roleCreds) (string, any, *http.Cookie, string) {
				if role == roleAdmin {
					u := newDisposableUser()
					return "/api/v1/admin/users/" + strconv.FormatInt(u.ID, 10), nil, nil, ""
				}
				return "/api/v1/admin/users/" + strconv.FormatInt(outsiderUser.ID, 10), nil, nil, ""
			},
		},
		"POST /api/v1/admin/users/{id}/password-reset": {
			expected: adminOK,
			makeReq:  constPath("/api/v1/admin/users/"+strconv.FormatInt(outsiderUser.ID, 10)+"/password-reset", nil),
		},
		"GET /api/v1/admin/projects":   {expected: adminOK, makeReq: constPath("/api/v1/admin/projects", nil)},
		"GET /api/v1/admin/settings":   {expected: adminOK, makeReq: constPath("/api/v1/admin/settings", nil)},
		"PATCH /api/v1/admin/settings": {expected: adminOK, makeReq: constPath("/api/v1/admin/settings", map[string]any{})},
		"GET /api/v1/admin/audit":      {expected: adminOK, makeReq: constPath("/api/v1/admin/audit", nil)},
		"GET /api/v1/admin/stats":      {expected: adminOK, makeReq: constPath("/api/v1/admin/stats", nil)},

		// Events & Render
		"GET /api/v1/events":         {expected: userOK, makeReq: constPath("/api/v1/events", nil)},
		"GET /render/{project}/{id}": {expected: viewerOK, makeReq: constPath("/render/"+proj.Slug+"/"+msgID, nil)},
	}

	// 1. Assert 1:1 coverage between srv.Routes() and matrix (FR-050 AC).
	registered := make(map[string]api.Route, len(env.srv.Routes()))
	for _, rt := range env.srv.Routes() {
		key := rt.Method + " " + rt.Pattern
		registered[key] = rt
		if _, ok := matrix[key]; !ok {
			t.Errorf("registered route %q is missing from TestAuthorizationMatrix", key)
		}
	}
	for key := range matrix {
		if _, ok := registered[key]; !ok {
			t.Errorf("matrix entry %q does not match any registered route", key)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	roleNames := map[authzRole]string{
		roleNone:       "none",
		roleOutsider:   "outsider",
		roleViewer:     "viewer",
		roleMember:     "member",
		roleOwner:      "owner",
		roleAdmin:      "admin",
		roleTokenRead:  "token-read",
		roleTokenWrite: "token-write",
	}
	orderedRoles := []authzRole{roleNone, roleOutsider, roleViewer, roleMember, roleOwner, roleAdmin, roleTokenRead, roleTokenWrite}

	// 2. Execute every route x role cell.
	for _, rt := range env.srv.Routes() {
		key := rt.Method + " " + rt.Pattern
		spec := matrix[key]

		for _, role := range orderedRoles {
			want, ok := spec.expected[role]
			if !ok {
				t.Fatalf("matrix[%q] missing expected status for role %s", key, roleNames[role])
			}
			creds := credsByRole[role]
			path, body, customCookie, customCSRF := spec.makeReq(t, role, creds)

			cookie := creds.cookie
			csrf := creds.csrf
			if customCookie != nil {
				cookie = customCookie
				csrf = customCSRF
			}

			var headers map[string]string
			if creds.bearer != "" {
				headers = map[string]string{"Authorization": "Bearer " + creds.bearer}
			} else if auth.IsUnsafeMethod(rt.Method) {
				headers = map[string]string{"Origin": testBaseURL}
				if cookie != nil && csrf != "" {
					headers["X-CSRF-Token"] = csrf
				}
			}

			var got int
			if key == "GET /api/v1/events" && want == http.StatusOK {
				got = probeSSEStatus(t, h, path, cookie, creds.bearer)
			} else {
				rec := doReq(t, h, rt.Method, path, body, cookie, headers)
				got = rec.Code
				if got != want {
					t.Errorf("%s [%s]: status = %d, want %d; body=%s", key, roleNames[role], got, want, rec.Body.String())
				}
				continue
			}
			if got != want {
				t.Errorf("%s [%s]: status = %d, want %d", key, roleNames[role], got, want)
			}
		}

		// 3. Also verify viewer with viewer_hide_technical=false for technical routes.
		if spec.viewerTechOff != 0 {
			techOffPath := strings.ReplaceAll(rt.Pattern, "{project}", projTechOff.Slug)
			techOffPath = strings.ReplaceAll(techOffPath, "{id}", msgTechOffID)
			rec := doReq(t, h, rt.Method, techOffPath, nil, credsByRole[roleViewer].cookie, nil)
			if rec.Code != spec.viewerTechOff {
				t.Errorf("%s [viewer, hide_tech=false]: status = %d, want %d; body=%s", key, rec.Code, spec.viewerTechOff, rec.Body.String())
			}
		}

		// 3b. Also verify member with members_can_reveal_smtp=true for credential reveal.
		if spec.memberRevealOn != 0 {
			revealPath := strings.ReplaceAll(rt.Pattern, "{project}", projTechOff.Slug)
			revealPath = strings.ReplaceAll(revealPath, "{id}", strconv.FormatInt(seedCredRevealID, 10))
			mc := credsByRole[roleMember]
			rec := doReq(t, h, rt.Method, revealPath, nil, mc.cookie, csrfHeaders(mc.csrf))
			if rec.Code != spec.memberRevealOn {
				t.Errorf("%s [member, members_can_reveal_smtp=true]: status = %d, want %d; body=%s", key, rec.Code, spec.memberRevealOn, rec.Body.String())
			}
		}
	}

	// 4. Verify all ServerOnly routes return 404 in local mode (SPEC 5.1, 8.1).
	localCfg, err := config.Load(nil)
	if err != nil {
		t.Fatalf("config.Load(local): %v", err)
	}
	localStore := testutil.NewStore(t)
	localBlobs, err := blob.NewStore(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("blob.NewStore(local): %v", err)
	}
	localSrv := api.New(localCfg, localStore, localBlobs, nil, api.Options{Version: "test"})
	defer localSrv.Close()

	for _, rt := range localSrv.Routes() {
		if !rt.ServerOnly {
			continue
		}
		p := strings.NewReplacer(
			"{project}", "default",
			"{id}", "1",
			"{user_id}", "1",
			"{token}", "tok",
		).Replace(rt.Pattern)
		rec := doReq(t, localSrv.Handler(), rt.Method, p, nil, nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("local mode ServerOnly route %s %s: status = %d, want 404", rt.Method, rt.Pattern, rec.Code)
		}
	}
}

func probeSSEStatus(t *testing.T, handler http.Handler, path string, cookie *http.Cookie, bearer string) int {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	sw := newSSEStreamWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(sw, req)
	}()
	sw.waitForContains(t, "retry: 3000")
	cancel()
	<-done
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.code
}
