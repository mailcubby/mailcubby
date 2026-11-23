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
	// PermUser requires any authenticated principal (session, token, or local operator).
	PermUser Permission = "user"
	// PermProjectCreate requires admin or can_create_projects (or local operator).
	PermProjectCreate Permission = "project_create"
	// PermViewer requires viewer role or higher on the resolved project.
	PermViewer Permission = "viewer"
	// PermViewerTech requires viewer role unless viewer_hide_technical is enabled (then member).
	PermViewerTech Permission = "viewer_tech"
	// PermMember requires member role or higher on the resolved project.
	PermMember Permission = "member"
	// PermOwner requires owner role or admin on the resolved project.
	PermOwner Permission = "owner"
)

// Route declares one HTTP endpoint in the single source-of-truth route table.
type Route struct {
	Method  string
	Pattern string
	Need    Permission
	Handler http.HandlerFunc
}

func (s *Server) buildRoutes() []Route {
	return []Route{
		// Health & OpenAPI (SPEC 8.9)
		{Method: http.MethodGet, Pattern: "/healthz", Need: PermPublic, Handler: s.handleHealthz},
		{Method: http.MethodGet, Pattern: "/readyz", Need: PermPublic, Handler: s.handleReadyz},
		{Method: http.MethodGet, Pattern: "/api/v1/openapi.yaml", Need: PermPublic, Handler: s.handleOpenAPI},

		// Meta & Auth (SPEC 8.2)
		{Method: http.MethodGet, Pattern: "/api/v1/meta", Need: PermPublic, Handler: s.handleMeta},
		{Method: http.MethodGet, Pattern: "/api/v1/auth/me", Need: PermUser, Handler: s.handleAuthMe},

		// Projects (SPEC 8.3)
		{Method: http.MethodGet, Pattern: "/api/v1/projects", Need: PermUser, Handler: s.handleListProjects},
		{Method: http.MethodPost, Pattern: "/api/v1/projects", Need: PermProjectCreate, Handler: s.handleCreateProject},
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}", Need: PermViewer, Handler: s.handleGetProject},
		{Method: http.MethodPatch, Pattern: "/api/v1/projects/{project}", Need: PermOwner, Handler: s.handleUpdateProject},
		{Method: http.MethodDelete, Pattern: "/api/v1/projects/{project}", Need: PermOwner, Handler: s.handleDeleteProject},

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
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/messages/{id}/headers", Need: PermViewerTech, Handler: s.handleGetMessageHeaders},
		{Method: http.MethodGet, Pattern: "/api/v1/projects/{project}/messages/{id}/attachments/{attachment_id}", Need: PermViewer, Handler: s.handleGetMessageAttachment},
		{Method: http.MethodPost, Pattern: "/api/v1/projects/{project}/send-test", Need: PermMember, Handler: s.handleSendTest},

		// Events & Render (SPEC 8.9)
		{Method: http.MethodGet, Pattern: "/api/v1/events", Need: PermUser, Handler: s.handleEvents},
		{Method: http.MethodGet, Pattern: "/render/{project}/{id}", Need: PermViewer, Handler: s.handleRenderMessage},
	}
}

func (s *Server) buildMux() http.Handler {
	apiMux := http.NewServeMux()
	for _, rt := range s.routes {
		h := rt.Handler
		if strings.Contains(rt.Pattern, "{project}") {
			h = s.resolveProjectMiddleware(h)
		}
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
