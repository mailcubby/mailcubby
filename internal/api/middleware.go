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
	"runtime/debug"
	"strings"

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
	ctxKeyPrincipal
	ctxKeyProject
)

// PrincipalKind identifies how an HTTP request was authenticated.
type PrincipalKind string

const (
	// PrincipalLocal represents the implicit operator in local mode (SPEC 5.1).
	PrincipalLocal PrincipalKind = "local"
	// PrincipalAnonymous represents an unauthenticated caller.
	PrincipalAnonymous PrincipalKind = "anonymous"
)

// Principal holds the authenticated caller identity in the request context.
type Principal struct {
	Kind              PrincipalKind
	UserID            int64
	Email             string
	Name              string
	IsAdmin           bool
	CanCreateProjects bool
}

// ProjectContext holds the resolved project and effective role for a project-scoped route.
type ProjectContext struct {
	Project store.Project
	Role    string
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
	v, ok := ctx.Value(ctxKeyPrincipal).(Principal)
	if !ok {
		return Principal{Kind: PrincipalAnonymous}
	}
	return v
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
	h = s.localPrincipalMiddleware(h)
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
					"path", r.URL.Path,
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
		next.ServeHTTP(rw, r)

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
		principal := PrincipalFromContext(r.Context())
		s.log.Log(r.Context(), level, "http request",
			"request_id", RequestIDFromContext(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", rw.bytes,
			"duration_ms", duration.Milliseconds(),
			"remote_ip", ClientIPFromContext(r.Context()),
			"principal", string(principal.Kind),
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

func (s *Server) localPrincipalMiddleware(next http.Handler) http.Handler {
	localOp := Principal{
		Kind:              PrincipalLocal,
		UserID:            0,
		Email:             "local@localhost",
		Name:              "Local Operator",
		IsAdmin:           true,
		CanCreateProjects: true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), ctxKeyPrincipal, localOp)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) resolveProjectMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		pctx := ProjectContext{
			Project: proj,
			Role:    "owner", // Local operator has owner rights on all projects (SPEC 5.1).
		}
		ctx := context.WithValue(r.Context(), ctxKeyProject, pctx)
		next(w, r.WithContext(ctx))
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
