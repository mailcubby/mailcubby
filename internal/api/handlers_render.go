package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/render"
)

// Render CSP policies mandated by SPEC 12.1.
const (
	renderCSPBlocked = "sandbox allow-popups allow-popups-to-escape-sandbox; " +
		"default-src 'none'; img-src data: cid:; style-src 'unsafe-inline'; " +
		"font-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
	renderCSPAllowImages = "sandbox allow-popups allow-popups-to-escape-sandbox; " +
		"default-src 'none'; img-src data: cid: https:; style-src 'unsafe-inline'; " +
		"font-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
)

func (s *Server) handleRenderMessage(w http.ResponseWriter, r *http.Request) {
	pctx, msg, ok := s.loadProjectMessage(w, r)
	if !ok {
		return
	}
	if !msg.HasHTML {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "message has no HTML part", nil)
		return
	}
	if s.renderer == nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "renderer is not configured", nil)
		return
	}

	res, err := s.renderer.Render(r.Context(), pctx.Project.ID, msg.ID, msg.BlobPath)
	if err != nil {
		if errors.Is(err, render.ErrNoHTML) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "message has no HTML part", nil)
			return
		}
		s.log.Error("render message html failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	csp := renderCSPBlocked
	if r.URL.Query().Get("images") == "1" && s.cfg.RenderRemoteImages == config.RenderRemoteImagesOptIn {
		csp = renderCSPAllowImages
	}

	// Replace app-level headers with the dedicated SPEC 12.1 render header set.
	// X-Frame-Options: DENY must be removed because /render is framed by 'self'.
	h := w.Header()
	h.Del("X-Frame-Options")
	h.Del("Permissions-Policy")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("X-DNS-Prefetch-Control", "off")
	h.Set("Content-Length", strconv.Itoa(len(res.HTML)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(res.HTML)
}
