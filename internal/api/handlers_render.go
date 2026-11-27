package api

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/render"
	"github.com/mailcubby/mailcubby/internal/store"
)

// Render CSP policies mandated by SPEC 12.1 and ADR 0009.
const (
	renderCSPBlocked = "sandbox allow-popups allow-popups-to-escape-sandbox; " +
		"default-src 'none'; img-src data: cid:; style-src 'unsafe-inline'; " +
		"font-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
	renderCSPAllowImages = "sandbox allow-popups allow-popups-to-escape-sandbox; " +
		"default-src 'none'; img-src data: cid: https:; style-src 'unsafe-inline'; " +
		"font-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
	renderPDFCSP = "default-src 'none'; object-src 'self'; style-src 'unsafe-inline'; " +
		"base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
	pdfMagicHeader = "%PDF-"
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

// handleRenderAttachment serves a validated PDF attachment inline under the
// dedicated PDF-specific CSP defined in ADR 0009 (FR-074, SPEC 12.2).
func (s *Server) handleRenderAttachment(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.RenderInlinePDF {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "inline PDF preview is disabled", nil)
		return
	}

	pctx, msg, ok := s.loadProjectMessage(w, r)
	if !ok {
		return
	}

	attID, err := strconv.ParseInt(r.PathValue("attachment_id"), 10, 64)
	if err != nil || attID <= 0 {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid attachment_id", nil)
		return
	}

	att, err := s.store.GetAttachmentByID(r.Context(), pctx.Project.ID, msg.PK, attID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "attachment not found", nil)
			return
		}
		s.log.Error("get attachment metadata for pdf render failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
			"attachment_id", attID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	// ADR 0009: strict coerced media-type validation before reading the blob.
	if safeAttachmentContentType(att.ContentType) != "application/pdf" {
		writeError(w, http.StatusUnsupportedMediaType, ErrCodeUnsupportedMediaType, "attachment is not a valid PDF", nil)
		return
	}

	rc, err := s.blobs.Open(msg.BlobPath)
	if err != nil {
		s.log.Error("open message blob for pdf render failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
			"attachment_id", attID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	data, extractErr := ingest.ExtractAttachmentContent(rc, att.PartPath, att.SHA256)
	_ = rc.Close()
	if extractErr != nil {
		if errors.Is(extractErr, ingest.ErrAttachmentNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "attachment part not found", nil)
			return
		}
		s.log.Error("extract attachment for pdf render failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
			"attachment_id", attID,
			"error", extractErr,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	// ADR 0009: require the %PDF- magic signature at byte offset 0 so forged
	// HTML/SVG/XML payloads or polyglots are rejected with 415.
	if !bytes.HasPrefix(data, []byte(pdfMagicHeader)) {
		writeError(w, http.StatusUnsupportedMediaType, ErrCodeUnsupportedMediaType, "attachment is not a valid PDF", nil)
		return
	}

	filename := strings.TrimSpace(att.Filename)
	if filename == "" {
		filename = fmt.Sprintf("attachment-%d", att.ID)
	}

	liftWriteDeadline(w)
	// Replace app-level headers with the dedicated ADR 0009 PDF render header set.
	// X-Frame-Options: DENY must be removed because /render is framed by 'self'.
	h := w.Header()
	h.Del("X-Frame-Options")
	h.Del("Permissions-Policy")
	h.Set("Content-Type", "application/pdf")
	h.Set("Content-Disposition", formatContentDisposition("inline", filename))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", renderPDFCSP)
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("X-DNS-Prefetch-Control", "off")
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
