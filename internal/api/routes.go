package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
)

// Permission describes the minimum caller role required by a route (SPEC 8.1, ARCHITECTURE 3.10).
type Permission string

const (
	// PermPublic requires no authentication.
	PermPublic Permission = "public"
	// PermSession requires a cookie-authenticated browser session (server mode).
	PermSession Permission = "session"
	// PermUser requires any authenticated principal (session, token, or local operator).
	PermUser Permission = "user"
	// PermProjectCreate requires admin or can_create_projects (or local operator).
	PermProjectCreate Permission = "project_create"
	// PermAdmin requires an instance administrator (server mode).
	PermAdmin Permission = "admin"
	// PermViewer requires viewer role or higher on the resolved project.
	PermViewer Permission = "viewer"
	// PermViewerTech requires viewer role unless viewer_hide_technical is enabled (then member).
	PermViewerTech Permission = "viewer_tech"
	// PermMember requires member role or higher on the resolved project.
	PermMember Permission = "member"
	// PermListMembers requires member role or higher to list project members (non-token).
	PermListMembers Permission = "list_members"
	// PermReadSMTP requires member role or higher to view SMTP credentials and connection info (non-token).
	PermReadSMTP Permission = "read_smtp"
	// PermRevealSMTP requires owner role or member when members_can_reveal_smtp is enabled (non-token).
	PermRevealSMTP Permission = "reveal_smtp"
	// PermManageSMTP requires owner role or admin to create/rotate/update/delete SMTP credentials (non-token).
	PermManageSMTP Permission = "manage_smtp"
	// PermOwner requires owner role or admin on the resolved project.
	PermOwner Permission = "owner"
	// PermProjectAudit requires owner role or admin to read a project's audit log (non-token).
	PermProjectAudit Permission = "project_audit"
	// PermLeave requires a session user with project access to leave the project (FR-051).
	PermLeave Permission = "leave"
)

// Route declares one HTTP endpoint in the single source-of-truth route table.
type Route struct {
	Method     string
	Pattern    string
	Need       Permission
	ServerOnly bool
	Handler    http.HandlerFunc
}

func (s *Server) buildRoutes() []Route {
	return []Route{
		// Health & OpenAPI (SPEC 8.9)
		{Method: http.MethodGet, Pattern: "/healthz", Need: PermPublic, Handler: s.handleHealthz},
		{Method: http.MethodGet, Pattern: "/readyz", Need: PermPublic, Handler: s.handleReadyz},
		{Method: http.MethodGet, Pattern: "/api/v1/openapi.yaml", Need: PermPublic, Handler: s.handleOpenAPI},

		// Meta, Setup & Auth (SPEC 8.2)
		{Method: http.MethodGet, Pattern: "/api/v1/meta", Need: PermPublic, Handler: s.handleMeta},
		{Method: http.MethodPost, Pattern: "/api/v1/setup", Need: PermPublic, ServerOnly: true, Handler: s.handleSetup},
		{Method: http.MethodPost, Pattern: "/api/v1/auth/login", Need: PermPublic, ServerOnly: true, Handler: s.handleAuthLogin},
		{Method: http.MethodPost, Pattern: "/api/v1/auth/logout", Need: PermSession, ServerOnly: true, Handler: s.handleAuthLogout},
		{Method: http.MethodGet, Pattern: "/api/v1/auth/me", Need: PermUser, Handler: s.handleAuthMe},
		{Method: http.MethodPatch, Pattern: "/api/v1/auth/me", Need: PermSession, ServerOnly: true, Handler: s.handleUpdateAuthMe},
		{Method: http.MethodGet, Pattern: "/api/v1/auth/sessions", Need: PermSession, ServerOnly: true, Handler: s.handleListAuthSessions},
		{Method: http.MethodDelete, Pattern: "/api/v1/auth/sessions/{id}", Need: PermSession, ServerOnly: true, Handler: s.handleDeleteAuthSession},
		{Method: http.MethodGet, Pattern: "/api/v1/invites/{token}", Need: PermPublic, ServerOnly: true, Handler: s.handlePreviewInvite},
		{Method: http.MethodPost, Pattern: "/api/v1/invites/{token}/accept", Need: PermPublic, ServerOnly: true, Handler: s.handleAcceptInvite},
		{Method: http.MethodPost, Pattern: "/api/v1/password-reset/{token}", Need: PermPublic, ServerOnly: true, Handler: s.handlePasswordReset},

		// Projects (SPEC 8.3)
		{Method: http.MethodGet, Pattern: "/api/v1/projects", Need: PermUser, Handler: s.handleListProjects},
		{Method: http.MethodPost, Pattern: "/api/v1/projects", Need: PermProjectCreate, Handler: s.handleCreateProject},
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}", Need: PermViewer, Handler: s.handleGetProject},
		{Method: http.MethodPatch, Pattern: "/api/v1/projects/{project}", Need: PermOwner, Handler: s.handleUpdateProject},
		{Method: http.MethodDelete, Pattern: "/api/v1/projects/{project}", Need: PermOwner, Handler: s.handleDeleteProject},
		{Method: http.MethodPost, Pattern: "/api/v1/projects/{project}/leave", Need: PermLeave, ServerOnly: true, Handler: s.handleLeaveProject},
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/audit", Need: PermProjectAudit, ServerOnly: true, Handler: s.handleListProjectAudit},

		// Messages (SPEC 8.4)
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/messages", Need: PermViewer, Handler: s.handleListMessages},
		{Method: http.MethodDelete, Pattern: "/api/v1/projects/{project}/messages", Need: PermMember, Handler: s.handleDeleteAllMessages},
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/messages/wait", Need: PermViewer, Handler: s.handleWaitMessage},
		{Method: http.MethodPost, Pattern: "/api/v1/projects/{project}/messages/bulk", Need: PermMember, Handler: s.handleBulkMessages},
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/messages/{id}", Need: PermViewer, Handler: s.handleGetMessage},
		{Method: http.MethodPatch, Pattern: "/api/v1/projects/{project}/messages/{id}", Need: PermMember, Handler: s.handlePatchMessage},
		{Method: http.MethodDelete, Pattern: "/api/v1/projects/{project}/messages/{id}", Need: PermMember, Handler: s.handleDeleteMessage},
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/messages/{id}/raw", Need: PermViewerTech, Handler: s.handleGetMessageRaw},
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/messages/{id}/eml", Need: PermViewerTech, Handler: s.handleGetMessageEML},
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/messages/{id}/html", Need: PermViewerTech, Handler: s.handleGetMessageHTML},
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/messages/{id}/html-check", Need: PermViewerTech, Handler: s.handleGetMessageHTMLCheck},
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/messages/{id}/headers", Need: PermViewerTech, Handler: s.handleGetMessageHeaders},
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/messages/{id}/attachments/{attachment_id}", Need: PermViewer, Handler: s.handleGetMessageAttachment},
		{Method: http.MethodPost, Pattern: "/api/v1/projects/{project}/send-test", Need: PermMember, Handler: s.handleSendTest},

		// Tags (SPEC 8.5)
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/tags", Need: PermViewer, Handler: s.handleListTags},
		{Method: http.MethodPost, Pattern: "/api/v1/projects/{project}/tags", Need: PermMember, Handler: s.handleCreateTag},
		{Method: http.MethodPatch, Pattern: "/api/v1/projects/{project}/tags/{id}", Need: PermMember, Handler: s.handleUpdateTag},
		{Method: http.MethodDelete, Pattern: "/api/v1/projects/{project}/tags/{id}", Need: PermMember, Handler: s.handleDeleteTag},

		// Members & Invites (SPEC 8.6)
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/members", Need: PermListMembers, ServerOnly: true, Handler: s.handleListProjectMembers},
		{Method: http.MethodPatch, Pattern: "/api/v1/projects/{project}/members/{user_id}", Need: PermOwner, ServerOnly: true, Handler: s.handleUpdateProjectMember},
		{Method: http.MethodDelete, Pattern: "/api/v1/projects/{project}/members/{user_id}", Need: PermOwner, ServerOnly: true, Handler: s.handleDeleteProjectMember},
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/invites", Need: PermOwner, ServerOnly: true, Handler: s.handleListProjectInvites},
		{Method: http.MethodPost, Pattern: "/api/v1/projects/{project}/invites", Need: PermOwner, ServerOnly: true, Handler: s.handleCreateProjectInvite},
		{Method: http.MethodDelete, Pattern: "/api/v1/projects/{project}/invites/{id}", Need: PermOwner, ServerOnly: true, Handler: s.handleRevokeProjectInvite},

		// SMTP Credentials (SPEC 8.6)
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/smtp-credentials", Need: PermReadSMTP, ServerOnly: true, Handler: s.handleListSMTPCredentials},
		{Method: http.MethodPost, Pattern: "/api/v1/projects/{project}/smtp-credentials", Need: PermManageSMTP, ServerOnly: true, Handler: s.handleCreateSMTPCredential},
		{Method: http.MethodPost, Pattern: "/api/v1/projects/{project}/smtp-credentials/{id}/reveal", Need: PermRevealSMTP, ServerOnly: true, Handler: s.handleRevealSMTPCredential},
		{Method: http.MethodPost, Pattern: "/api/v1/projects/{project}/smtp-credentials/{id}/rotate", Need: PermManageSMTP, ServerOnly: true, Handler: s.handleRotateSMTPCredential},
		{Method: http.MethodPatch, Pattern: "/api/v1/projects/{project}/smtp-credentials/{id}", Need: PermManageSMTP, ServerOnly: true, Handler: s.handleUpdateSMTPCredential},
		{Method: http.MethodDelete, Pattern: "/api/v1/projects/{project}/smtp-credentials/{id}", Need: PermManageSMTP, ServerOnly: true, Handler: s.handleDeleteSMTPCredential},

		// API Tokens (SPEC 8.7)
		{Method: http.MethodGet, Pattern: "/api/v1/tokens", Need: PermSession, ServerOnly: true, Handler: s.handleListAPITokens},
		{Method: http.MethodPost, Pattern: "/api/v1/tokens", Need: PermSession, ServerOnly: true, Handler: s.handleCreateAPIToken},
		{Method: http.MethodDelete, Pattern: "/api/v1/tokens/{id}", Need: PermSession, ServerOnly: true, Handler: s.handleDeleteAPIToken},

		// Admin (SPEC 8.8)
		{Method: http.MethodGet, Pattern: "/api/v1/admin/users", Need: PermAdmin, ServerOnly: true, Handler: s.handleAdminListUsers},
		{Method: http.MethodPost, Pattern: "/api/v1/admin/users", Need: PermAdmin, ServerOnly: true, Handler: s.handleAdminCreateUser},
		{Method: http.MethodPatch, Pattern: "/api/v1/admin/users/{id}", Need: PermAdmin, ServerOnly: true, Handler: s.handleAdminUpdateUser},
		{Method: http.MethodDelete, Pattern: "/api/v1/admin/users/{id}", Need: PermAdmin, ServerOnly: true, Handler: s.handleAdminDeleteUser},
		{Method: http.MethodPost, Pattern: "/api/v1/admin/users/{id}/password-reset", Need: PermAdmin, ServerOnly: true, Handler: s.handleAdminPasswordReset},
		{Method: http.MethodGet, Pattern: "/api/v1/admin/projects", Need: PermAdmin, ServerOnly: true, Handler: s.handleAdminListProjects},
		{Method: http.MethodGet, Pattern: "/api/v1/admin/settings", Need: PermAdmin, ServerOnly: true, Handler: s.handleAdminGetSettings},
		{Method: http.MethodPatch, Pattern: "/api/v1/admin/settings", Need: PermAdmin, ServerOnly: true, Handler: s.handleAdminUpdateSettings},
		{Method: http.MethodGet, Pattern: "/api/v1/admin/audit", Need: PermAdmin, ServerOnly: true, Handler: s.handleAdminListAudit},
		{Method: http.MethodGet, Pattern: "/api/v1/admin/stats", Need: PermAdmin, ServerOnly: true, Handler: s.handleAdminStats},

		// Events & Render (SPEC 8.9)
		{Method: http.MethodGet, Pattern: "/api/v1/events", Need: PermUser, Handler: s.handleEvents},
		{Method: http.MethodGet, Pattern: "/render/{project}/{id}", Need: PermViewer, Handler: s.handleRenderMessage},
		{Method: http.MethodGet, Pattern: "/render/{project}/{id}/attachments/{attachment_id}", Need: PermViewer, Handler: s.handleRenderAttachment},
	}
}

func (s *Server) buildMux() http.Handler {
	apiMux := http.NewServeMux()
	for _, rt := range s.routes {
		h := rt.Handler
		if strings.Contains(rt.Pattern, "{project}") {
			h = s.resolveProjectMiddleware(rt, h)
		}
		h = s.authorizeRouteMiddleware(rt, h)
		apiMux.HandleFunc(rt.Method+" "+rt.Pattern, h)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := apiMux.Handler(r)
		if pattern != "" {
			apiMux.ServeHTTP(w, r)
			return
		}

		// Preserve JSON errors (404/405) for unmatched /api and /render paths instead of plain text or SPA fallback.
		if isAPIPath(r.URL.Path) || isRenderPath(r.URL.Path) || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			rec := httptest.NewRecorder()
			apiMux.ServeHTTP(rec, r)
			if rec.Code == http.StatusMethodNotAllowed {
				if allow := rec.Header().Get("Allow"); allow != "" {
					w.Header().Set("Allow", allow)
				}
				writeError(w, http.StatusMethodNotAllowed, ErrCodeMethodNotAllowed, "method not allowed", nil)
				return
			}
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "endpoint not found", nil)
			return
		}

		if s.webHandler != nil {
			s.webHandler.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

func isAPIPath(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/")
}

func isRenderPath(p string) bool {
	return p == "/render" || strings.HasPrefix(p, "/render/")
}
