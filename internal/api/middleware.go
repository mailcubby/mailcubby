package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"path"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/ratelimit"
	"github.com/mailcubby/mailcubby/internal/store"
)

// Security headers mandated by SPEC 12.3 and SPEC 12.2.
const (
	appCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; " +
		"frame-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"
	attachmentCSP     = "sandbox; default-src 'none'"
	permissionsPolicy = "camera=(), microphone=(), geolocation=()"
	hstsValue         = "max-age=31536000; includeSubDomains"
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota + 1
	ctxKeyClientIP
	ctxKeyProject
	ctxKeyLogMeta
)

type requestLogMeta struct {
	principalKind auth.PrincipalKind
}

// PrincipalKind identifies how an HTTP request was authenticated.
type PrincipalKind = auth.PrincipalKind

// Principal holds the authenticated caller identity in the request context.
type Principal = auth.Principal

const (
	// PrincipalLocal represents the implicit operator in local mode (SPEC 5.1).
	PrincipalLocal = auth.PrincipalLocal
	// PrincipalUser represents a browser session authenticated via cookie (SPEC FR-044).
	PrincipalUser = auth.PrincipalUser
	// PrincipalToken represents an API bearer token caller (SPEC FR-065).
	PrincipalToken = auth.PrincipalToken
	// PrincipalAnonymous represents an unauthenticated caller.
	PrincipalAnonymous = auth.PrincipalAnonymous
)

// ProjectContext holds the resolved project and effective role for a project-scoped route.
type ProjectContext struct {
	Project          store.Project
	Role             string
	AuthRole         auth.Role
	UserAuthRole     auth.Role
	IsExplicitMember bool
}

// AuthSettings extracts the permission-relevant project settings for auth.Allowed.
func (p ProjectContext) AuthSettings() auth.ProjectSettings {
	return auth.ProjectSettings{
		ViewerHideTechnical:  p.Project.Settings.ViewerHideTechnical,
		MembersCanRevealSMTP: p.Project.Settings.MembersCanRevealSMTP,
	}
}

// CanReadTechnical reports whether this request may read technical message views
// (SPEC FR-066, FR-071): viewer_hide_technical applies only when the caller's own
// project role is viewer (a member's read token still sees technical views).
func (p ProjectContext) CanReadTechnical() bool {
	userRole := p.UserAuthRole
	if userRole == auth.RoleNone {
		userRole = p.AuthRole
	}
	return auth.CanReadTechnical(p.AuthRole, userRole, p.AuthSettings())
}

// RequestIDFromContext returns the request ID attached by middleware, if any.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRequestID).(string)
	return v
}

// ClientIPFromContext returns the resolved client IP attached by middleware, if any.
func ClientIPFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyClientIP).(string)
	return v
}

// PrincipalFromContext returns the Principal attached by middleware.
func PrincipalFromContext(ctx context.Context) Principal {
	return auth.PrincipalFromContext(ctx)
}

// ProjectFromContext returns the ProjectContext attached by project-resolution middleware.
func ProjectFromContext(ctx context.Context) (ProjectContext, bool) {
	v, ok := ctx.Value(ctxKeyProject).(ProjectContext)
	return v, ok
}

// RedactHeaders returns a copy of header values with sensitive authentication and
// CSRF headers replaced by "[REDACTED]" (SPEC 12.3).
func RedactHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		if isSensitiveHeader(k) {
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = strings.Join(vals, ", ")
	}
	return out
}

// secretPathPrefixes are URL path prefixes whose next segment is a bearer
// secret (invite or password-reset token) that must never reach the logs.
var secretPathPrefixes = [...]string{
	"/api/v1/invites/",
	"/api/v1/password-reset/",
	"/invite/",
	"/reset/",
}

// RedactLogPath returns p with the token segment of invite and password-reset
// paths (API and SPA) replaced by "[REDACTED]", e.g.
// "/api/v1/invites/[REDACTED]/accept". Matching uses the cleaned path and is
// case-insensitive so variants such as "//Invite/<token>" are redacted too.
// Other paths are returned unchanged.
func RedactLogPath(p string) string {
	cleaned := path.Clean("/" + p)
	for _, prefix := range secretPathPrefixes {
		if len(cleaned) <= len(prefix) || !strings.EqualFold(cleaned[:len(prefix)], prefix) {
			continue
		}
		rest := cleaned[len(prefix):]
		suffix := ""
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			suffix = rest[i:]
		}
		return prefix + "[REDACTED]" + suffix
	}
	return p
}

func isSensitiveHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "cookie", "set-cookie", "x-csrf-token":
		return true
	default:
		return false
	}
}

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	h := next
	h = s.csrfMiddleware(h)
	h = s.apiRateLimitMiddleware(h)
	h = s.authenticateMiddleware(h)
	h = s.bodyLimitMiddleware(h)
	h = s.accessLogMiddleware(h)
	h = s.securityHeadersMiddleware(h)
	h = s.realIPMiddleware(h)
	h = s.requestIDMiddleware(h)
	h = s.recoverMiddleware(h)
	return h
}

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := newTrackedResponseWriter(w)
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("http handler panic recovered",
					"request_id", RequestIDFromContext(r.Context()),
					"method", r.Method,
					"path", RedactLogPath(r.URL.Path),
					"panic", fmt.Sprint(rec),
					"stack", string(debug.Stack()),
				)
				if !rw.wroteHeader {
					writeError(rw, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
				}
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

func (s *Server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := newRequestID()
		w.Header().Set("X-Request-ID", reqID)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) realIPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := resolveClientIP(r.RemoteAddr, r.Header.Values("X-Forwarded-For"), s.cfg.TrustedProxies)
		ctx := context.WithValue(r.Context(), ctxKeyClientIP, ip)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	sendHSTS := s.cfg.HSTS && strings.HasPrefix(strings.ToLower(s.cfg.BaseURL), "https://")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", appCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", permissionsPolicy)
		if sendHSTS {
			h.Set("Strict-Transport-Security", hstsValue)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.clk.Now()
		rw := newTrackedResponseWriter(w)
		meta := &requestLogMeta{principalKind: auth.PrincipalAnonymous}
		ctx := context.WithValue(r.Context(), ctxKeyLogMeta, meta)
		next.ServeHTTP(rw, r.WithContext(ctx))

		status := rw.status
		if status == 0 {
			status = http.StatusOK
		}
		duration := s.clk.Now().Sub(start)

		// FR-110: /healthz and /readyz must not log at INFO.
		level := slog.LevelInfo
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			level = slog.LevelDebug
		}

		// Log path without query string and never log Authorization/Cookie/X-CSRF-Token.
		s.log.Log(r.Context(), level, "http request",
			"request_id", RequestIDFromContext(r.Context()),
			"method", r.Method,
			"path", RedactLogPath(r.URL.Path),
			"status", status,
			"bytes", rw.bytes,
			"duration_ms", duration.Milliseconds(),
			"remote_ip", ClientIPFromContext(r.Context()),
			"principal", string(meta.principalKind),
		)
	})
}

func (s *Server) bodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodySize)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authenticateMiddleware(next http.Handler) http.Handler {
	localOp := auth.LocalPrincipal()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var principal auth.Principal
		if s.cfg.Mode == config.ModeLocal {
			principal = localOp
		} else {
			principal = s.authenticateServerRequest(r)
		}
		if meta, ok := r.Context().Value(ctxKeyLogMeta).(*requestLogMeta); ok {
			meta.principalKind = principal.Kind
		}
		ctx := auth.WithPrincipal(r.Context(), principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) authenticateServerRequest(r *http.Request) auth.Principal {
	if s.store == nil {
		return auth.Principal{Kind: auth.PrincipalAnonymous}
	}

	// SPEC FR-065: Authorization: Bearer <token> takes precedence and never falls back to cookie.
	if rawBearer, ok := extractBearerToken(r.Header.Get("Authorization")); ok {
		return s.authenticateBearerToken(r, rawBearer)
	}

	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return auth.Principal{Kind: auth.PrincipalAnonymous}
	}

	sessionID := auth.HashToken(cookie.Value)
	sess, err := s.store.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		return auth.Principal{Kind: auth.PrincipalAnonymous}
	}

	now := s.clk.Now().UTC()
	if auth.IsSessionExpired(now, sess.LastSeenAt, sess.ExpiresAt, s.cfg.SessionIdleTimeout) {
		_ = s.store.DeleteSessionByID(r.Context(), sess.ID)
		return auth.Principal{Kind: auth.PrincipalAnonymous}
	}

	user, err := s.store.GetUserByID(r.Context(), sess.UserID)
	if err != nil || user.DisabledAt != nil {
		return auth.Principal{Kind: auth.PrincipalAnonymous}
	}

	if auth.NeedsSessionTouch(now, sess.LastSeenAt) {
		if err := s.store.TouchSession(r.Context(), sess.ID, now); err != nil {
			s.log.Warn("failed to update session last_seen_at",
				"request_id", RequestIDFromContext(r.Context()),
				"error", err,
			)
		}
	}

	return auth.Principal{
		Kind:              auth.PrincipalUser,
		UserID:            user.ID,
		Email:             user.Email,
		Name:              user.Name,
		IsAdmin:           user.IsAdmin,
		CanCreateProjects: user.CanCreateProjects,
		SessionID:         sess.ID,
		CSRFToken:         sess.CSRFToken,
	}
}

func (s *Server) authenticateBearerToken(r *http.Request, raw string) auth.Principal {
	prefix, ok := auth.ParseAPIToken(raw)
	if !ok {
		return auth.Principal{Kind: auth.PrincipalAnonymous}
	}
	tok, err := s.store.GetAPITokenByPrefix(r.Context(), prefix)
	if err != nil {
		return auth.Principal{Kind: auth.PrincipalAnonymous}
	}
	if !auth.VerifyAPITokenHash(raw, tok.TokenHash) {
		return auth.Principal{Kind: auth.PrincipalAnonymous}
	}
	if tok.RevokedAt != nil {
		return auth.Principal{Kind: auth.PrincipalAnonymous}
	}
	now := s.clk.Now().UTC()
	if tok.ExpiresAt != nil && !now.Before(*tok.ExpiresAt) {
		return auth.Principal{Kind: auth.PrincipalAnonymous}
	}

	user, err := s.store.GetUserByID(r.Context(), tok.UserID)
	if err != nil || user.DisabledAt != nil {
		return auth.Principal{Kind: auth.PrincipalAnonymous}
	}

	if auth.NeedsAPITokenTouch(now, tok.LastUsedAt) {
		if err := s.store.TouchAPITokenLastUsed(r.Context(), tok.ID, now); err != nil {
			s.log.Warn("failed to update api token last_used_at",
				"request_id", RequestIDFromContext(r.Context()),
				"token_id", tok.ID,
				"error", err,
			)
		}
	}

	return auth.Principal{
		Kind:              auth.PrincipalToken,
		UserID:            user.ID,
		Email:             user.Email,
		Name:              user.Name,
		IsAdmin:           user.IsAdmin,
		CanCreateProjects: user.CanCreateProjects,
		TokenID:           tok.ID,
		TokenPrefix:       tok.Prefix,
		TokenScope:        tok.Scope,
		TokenProjectID:    tok.ProjectID,
		TokenProjectSlug:  tok.ProjectSlug,
	}
}

func extractBearerToken(header string) (string, bool) {
	trimmed := strings.TrimSpace(header)
	if len(trimmed) < len("Bearer ") {
		return "", false
	}
	if !strings.EqualFold(trimmed[:len("Bearer ")], "Bearer ") {
		return "", false
	}
	return strings.TrimSpace(trimmed[len("Bearer "):]), true
}

// apiRateLimitMiddleware enforces MAILCUBBY_API_RATE_LIMIT (SPEC 9.2, 12.4):
// per session or per API token in server mode on API/render endpoints. Local
// mode and unauthenticated endpoints (which have dedicated IP limiters) are exempt.
func (s *Server) apiRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Mode != config.ModeServer || s.apiLimiter == nil {
			next.ServeHTTP(w, r)
			return
		}
		if !isAPIPath(r.URL.Path) && !isRenderPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		principal := PrincipalFromContext(r.Context())
		var key string
		switch principal.Kind {
		case auth.PrincipalUser:
			key = "session:" + principal.SessionID
		case auth.PrincipalToken:
			key = "token:" + strconv.FormatInt(principal.TokenID, 10)
		default:
			next.ServeHTTP(w, r)
			return
		}

		if allowed, retryAfter := s.apiLimiter.Allow(key); !allowed {
			sec := ratelimit.RetryAfterSeconds(retryAfter)
			w.Header().Set("Retry-After", strconv.Itoa(sec))
			writeError(w, http.StatusTooManyRequests, ErrCodeRateLimited, "rate limit exceeded, try again later", map[string]string{
				"retry_after": strconv.Itoa(sec),
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isPublicUnsafePath(p string) bool {
	cleaned := path.Clean("/" + p)
	return cleaned == "/api/v1/setup" ||
		cleaned == "/api/v1/auth/login" ||
		strings.HasPrefix(cleaned, "/api/v1/invites/") ||
		strings.HasPrefix(cleaned, "/api/v1/password-reset/")
}

// csrfMiddleware enforces SPEC FR-045 in server mode:
//   - All unsafe requests require Origin (or Referer fallback) matching MAILCUBBY_BASE_URL.
//   - Cookie-authenticated unsafe requests additionally require X-CSRF-Token.
//   - Bearer-token requests are exempt; invalid/revoked Bearer headers on non-public
//     routes pass through so route auth returns 401 rather than 403.
func (s *Server) csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Mode != config.ModeServer || !auth.IsUnsafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		principal := PrincipalFromContext(r.Context())
		if principal.Kind == auth.PrincipalToken {
			next.ServeHTTP(w, r)
			return
		}
		if _, hasBearer := extractBearerToken(r.Header.Get("Authorization")); hasBearer &&
			principal.Kind == auth.PrincipalAnonymous &&
			!isPublicUnsafePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !auth.CheckOrigin(r, s.cfg.BaseURL) {
			writeError(w, http.StatusForbidden, ErrCodeForbidden, "invalid request origin", nil)
			return
		}
		if principal.Kind == auth.PrincipalUser {
			got := r.Header.Get(auth.CSRFHeaderName)
			if !auth.CheckCSRFToken(got, principal.CSRFToken) {
				writeError(w, http.StatusForbidden, ErrCodeForbidden, "invalid CSRF token", nil)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorizeRouteMiddleware(rt Route, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rt.ServerOnly && s.cfg.Mode == config.ModeLocal {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "endpoint not found", nil)
			return
		}
		if s.cfg.Mode == config.ModeLocal {
			next(w, r)
			return
		}

		principal := PrincipalFromContext(r.Context())
		switch rt.Need {
		case PermPublic:
			next(w, r)
			return
		case PermSession:
			if principal.Kind == auth.PrincipalAnonymous {
				writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "authentication required", nil)
				return
			}
			if principal.Kind != auth.PrincipalUser {
				writeError(w, http.StatusForbidden, ErrCodeForbidden, "insufficient permissions", nil)
				return
			}
		case PermUser:
			if !principal.IsAuthenticated() {
				writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "authentication required", nil)
				return
			}
		case PermProjectCreate:
			if !principal.IsAuthenticated() {
				writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "authentication required", nil)
				return
			}
			if principal.Kind == auth.PrincipalToken || (!principal.IsAdmin && !principal.CanCreateProjects) {
				writeError(w, http.StatusForbidden, ErrCodeForbidden, "insufficient permissions", nil)
				return
			}
		case PermAdmin:
			if !principal.IsAuthenticated() {
				writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "authentication required", nil)
				return
			}
			if principal.Kind != auth.PrincipalUser || !principal.IsAdmin {
				writeError(w, http.StatusForbidden, ErrCodeForbidden, "insufficient permissions", nil)
				return
			}
		case PermLeave:
			if !principal.IsAuthenticated() {
				writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "authentication required", nil)
				return
			}
			if principal.Kind != auth.PrincipalUser {
				writeError(w, http.StatusForbidden, ErrCodeForbidden, "insufficient permissions", nil)
				return
			}
		case PermListMembers, PermReadSMTP, PermRevealSMTP, PermManageSMTP, PermOwner, PermProjectAudit:
			if !principal.IsAuthenticated() {
				writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "authentication required", nil)
				return
			}
			if principal.Kind == auth.PrincipalToken {
				writeError(w, http.StatusForbidden, ErrCodeForbidden, "insufficient permissions", nil)
				return
			}
		case PermViewer, PermViewerTech, PermMember:
			if !principal.IsAuthenticated() {
				writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "authentication required", nil)
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) resolveProjectMiddleware(rt Route, next http.HandlerFunc) http.HandlerFunc {
	action, hasAction := permissionAction(rt.Need)
	return func(w http.ResponseWriter, r *http.Request) {
		principal := PrincipalFromContext(r.Context())

		slug := r.PathValue("project")
		if slug == "" {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
			return
		}
		proj, err := s.store.GetProjectBySlug(r.Context(), slug)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
				return
			}
			s.log.Error("resolve project failed",
				"request_id", RequestIDFromContext(r.Context()),
				"slug", slug,
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}

		var (
			authRole         auth.Role
			userAuthRole     auth.Role
			displayRole      string
			isExplicitMember bool
		)
		if s.cfg.Mode == config.ModeLocal {
			authRole = auth.RoleOwner
			userAuthRole = auth.RoleOwner
			displayRole = string(auth.RoleOwner)
			isExplicitMember = true
		} else {
			// FR-065/FR-066: project-restricted tokens cannot see other projects (404).
			if principal.Kind == auth.PrincipalToken && principal.TokenProjectID != nil && *principal.TokenProjectID != proj.ID {
				writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
				return
			}

			member, memErr := s.store.GetProjectMember(r.Context(), proj.ID, principal.UserID)
			switch {
			case memErr == nil:
				isExplicitMember = true
				userAuthRole = auth.Role(member.Role)
				displayRole = member.Role
			case errors.Is(memErr, store.ErrNotFound):
				isExplicitMember = false
			default:
				s.log.Error("resolve project membership failed",
					"request_id", RequestIDFromContext(r.Context()),
					"project_id", proj.ID,
					"user_id", principal.UserID,
					"error", memErr,
				)
				writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
				return
			}

			if principal.IsAdmin {
				userAuthRole = auth.RoleAdmin
				displayRole = string(auth.RoleOwner)
				if !isExplicitMember {
					s.recordAdminProjectAccess(r.Context(), principal, proj)
				}
			} else if !isExplicitMember {
				// SPEC FR-050: inaccessible projects return 404 so project existence is not revealed.
				writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
				return
			}

			authRole = userAuthRole
			if principal.Kind == auth.PrincipalToken {
				authRole = auth.EffectiveTokenRole(userAuthRole, principal.TokenScope)
				displayRole = string(authRole)
			}

			if hasAction {
				settings := auth.ProjectSettings{
					ViewerHideTechnical:  proj.Settings.ViewerHideTechnical,
					MembersCanRevealSMTP: proj.Settings.MembersCanRevealSMTP,
				}
				var allowed bool
				if action == auth.ActionReadTechnical {
					allowed = auth.CanReadTechnical(authRole, userAuthRole, settings)
				} else {
					allowed = auth.Allowed(authRole, action, settings)
				}
				if !allowed {
					writeError(w, http.StatusForbidden, ErrCodeForbidden, "insufficient permissions", nil)
					return
				}
			}
		}

		pctx := ProjectContext{
			Project:          proj,
			Role:             displayRole,
			AuthRole:         authRole,
			UserAuthRole:     userAuthRole,
			IsExplicitMember: isExplicitMember,
		}
		ctx := context.WithValue(r.Context(), ctxKeyProject, pctx)
		next(w, r.WithContext(ctx))
	}
}

func permissionAction(p Permission) (auth.Action, bool) {
	switch p {
	case PermViewer:
		return auth.ActionReadMessages, true
	case PermViewerTech:
		return auth.ActionReadTechnical, true
	case PermMember:
		return auth.ActionWriteMessages, true
	case PermListMembers:
		return auth.ActionListMembers, true
	case PermReadSMTP:
		return auth.ActionReadSMTP, true
	case PermRevealSMTP:
		return auth.ActionRevealSMTP, true
	case PermManageSMTP:
		return auth.ActionManageSMTP, true
	case PermOwner:
		return auth.ActionManageProject, true
	case PermProjectAudit:
		return auth.ActionReadProjectAudit, true
	case PermLeave:
		return auth.ActionLeaveProject, true
	default:
		return "", false
	}
}

func resolveClientIP(remoteAddr string, xffValues []string, trustedProxies []netip.Prefix) string {
	peerAddr, ok := parseHostOrAddr(remoteAddr)
	if !ok {
		return remoteAddr
	}
	if !isTrustedProxy(peerAddr, trustedProxies) || len(xffValues) == 0 {
		return peerAddr.String()
	}

	var chain []netip.Addr
	for _, hdr := range xffValues {
		for _, part := range strings.Split(hdr, ",") {
			if addr, valid := parseHostOrAddr(strings.TrimSpace(part)); valid {
				chain = append(chain, addr)
			}
		}
	}
	if len(chain) == 0 {
		return peerAddr.String()
	}

	// Walk right-to-left and pick the rightmost hop not in MAILCUBBY_TRUSTED_PROXIES (SPEC 12.4).
	for i := len(chain) - 1; i >= 0; i-- {
		if !isTrustedProxy(chain[i], trustedProxies) {
			return chain[i].String()
		}
	}
	return chain[0].String()
}

func parseHostOrAddr(raw string) (netip.Addr, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func isTrustedProxy(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type trackedResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func newTrackedResponseWriter(w http.ResponseWriter) *trackedResponseWriter {
	return &trackedResponseWriter{ResponseWriter: w}
}

func (w *trackedResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *trackedResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *trackedResponseWriter) Flush() {
	if fl, ok := w.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

func (w *trackedResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
