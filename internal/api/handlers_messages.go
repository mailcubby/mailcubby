package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/compat"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/render"
	"github.com/mailcubby/mailcubby/internal/search"
	"github.com/mailcubby/mailcubby/internal/store"
)

const (
	defaultPageLimit             = 50
	maxPageLimit                 = 200
	maxDetailTextBytes           = 1 << 20 // 1 MiB (SPEC 8.4)
	maxEMLFilenameRunes          = 80
	defaultAttachmentExt         = "download"
	defaultAttachmentContentType = "application/octet-stream"
)

// activeContentTypes must always be served as application/octet-stream (SPEC 12.2).
// Any */xml, *+xml, multipart/*, or invalid Content-Type is also coerced by
// safeAttachmentContentType.
var activeContentTypes = map[string]struct{}{
	"text/html":                {},
	"application/xhtml+xml":    {},
	"image/svg+xml":            {},
	"text/xml":                 {},
	"application/xml":          {},
	"application/javascript":   {},
	"text/javascript":          {},
	"application/x-javascript": {},
	"text/ecmascript":          {},
	"application/ecmascript":   {},
}

// inlineRasterImageTypes are the only MIME types allowed to honour ?inline=1 (SPEC 12.2, FR-074).
var inlineRasterImageTypes = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/gif":  {},
	"image/webp": {},
}

type addressResponse struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type messageSummaryResponse struct {
	ID              string            `json:"id"`
	ReceivedAt      Timestamp         `json:"received_at"`
	From            addressResponse   `json:"from"`
	To              []addressResponse `json:"to"`
	Cc              []addressResponse `json:"cc"`
	Subject         string            `json:"subject"`
	Snippet         string            `json:"snippet"`
	SizeBytes       int64             `json:"size_bytes"`
	Seen            bool              `json:"seen"`
	AttachmentCount int64             `json:"attachment_count"`
	HasHTML         bool              `json:"has_html"`
	HasText         bool              `json:"has_text"`
	Tags            []string          `json:"tags"`
	CredentialLabel string            `json:"credential_label,omitempty"`
}

type messageListResponse struct {
	Items      []messageSummaryResponse `json:"items"`
	NextCursor *string                  `json:"next_cursor"`
}

type messageEnvelopeResponse struct {
	MailFrom string   `json:"mail_from"`
	RcptTo   []string `json:"rcpt_to"`
	RemoteIP *string  `json:"remote_ip"`
	HELO     *string  `json:"helo"`
	TLS      bool     `json:"tls"`
	AuthUser *string  `json:"auth_user"`
}

type attachmentResponse struct {
	ID               int64   `json:"id"`
	Filename         string  `json:"filename"`
	ContentType      string  `json:"content_type"`
	SizeBytes        int64   `json:"size_bytes"`
	ContentID        *string `json:"content_id"`
	Disposition      string  `json:"disposition"`
	ReferencedByHTML bool    `json:"referenced_by_html"`
}

type messageDetailResponse struct {
	messageSummaryResponse
	ReplyTo             []addressResponse        `json:"reply_to"`
	Bcc                 []addressResponse        `json:"bcc"`
	Envelope            *messageEnvelopeResponse `json:"envelope,omitempty"`
	MessageIDHeader     *string                  `json:"message_id_header"`
	DateHeader          *Timestamp               `json:"date_header"`
	Text                string                   `json:"text"`
	TextGenerated       bool                     `json:"text_generated"`
	TextTruncated       bool                     `json:"text_truncated"`
	Attachments         []attachmentResponse     `json:"attachments"`
	RenderURL           *string                  `json:"render_url"`
	RemoteResourceCount int                      `json:"remote_resource_count"`
	ParseWarnings       *[]string                `json:"parse_warnings,omitempty"`
	Headers             *[]ingest.Header         `json:"headers,omitempty"`
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	q := r.URL.Query()
	limit := int64(defaultPageLimit)
	if rawLimit := strings.TrimSpace(q.Get("limit")); rawLimit != "" {
		parsed, err := strconv.ParseInt(rawLimit, 10, 64)
		if err != nil || parsed < 1 || parsed > maxPageLimit {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "limit must be an integer between 1 and 200", nil)
			return
		}
		limit = parsed
	}

	var beforeID string
	if rawCursor := strings.TrimSpace(q.Get("cursor")); rawCursor != "" {
		decoded, err := decodeMessageCursor(rawCursor)
		if err != nil {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid cursor parameter", nil)
			return
		}
		beforeID = decoded
	}

	rawQuery := strings.TrimSpace(q.Get("query"))
	rawTag := strings.TrimSpace(q.Get("tag"))
	var credentialID *int64
	if rawCred := strings.TrimSpace(q.Get("credential")); rawCred != "" {
		parsedCred, credErr := parsePositiveInt64Param(rawCred)
		if credErr != nil {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "credential must be a positive integer", nil)
			return
		}
		credentialID = &parsedCred
	}

	var (
		msgs []store.Message
		err  error
	)
	if rawQuery == "" && rawTag == "" && credentialID == nil {
		msgs, err = s.store.ListMessagesPage(r.Context(), pctx.Project.ID, beforeID, limit+1)
	} else {
		parsedQuery, parseErr := search.Parse(rawQuery)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, parseErr.Error(), nil)
			return
		}
		msgs, err = search.Messages(r.Context(), s.store, pctx.Project.ID, parsedQuery, search.BuildOptions{
			Tag:          rawTag,
			CredentialID: credentialID,
			BeforeID:     beforeID,
			Limit:        limit + 1,
		})
	}
	if err != nil {
		s.log.Error("list messages failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	var nextCursor *string
	if int64(len(msgs)) > limit {
		msgs = msgs[:limit]
		cur := encodeMessageCursor(msgs[len(msgs)-1].ID)
		nextCursor = &cur
	}

	pks := make([]int64, 0, len(msgs))
	for _, m := range msgs {
		pks = append(pks, m.PK)
	}
	tagsByPK, err := s.store.ListTagsByMessagePKs(r.Context(), pctx.Project.ID, pks)
	if err != nil {
		s.log.Error("list message tags failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	credLabels, err := s.credentialLabelsForMessages(r.Context(), pctx.Project.ID, msgs)
	if err != nil {
		s.log.Error("list credential labels failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	items := make([]messageSummaryResponse, 0, len(msgs))
	for _, m := range msgs {
		var label string
		if m.SMTPCredentialID != nil {
			label = credLabels[*m.SMTPCredentialID]
		}
		items = append(items, toMessageSummary(m, tagsByPK[m.PK], label))
	}

	writeJSON(w, http.StatusOK, messageListResponse{
		Items:      items,
		NextCursor: nextCursor,
	})
}

func (s *Server) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	pctx, msg, ok := s.loadProjectMessage(w, r)
	if !ok {
		return
	}

	atts, err := s.store.ListAttachmentsByMessagePK(r.Context(), pctx.Project.ID, msg.PK)
	if err != nil {
		s.log.Error("list attachments failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	rc, err := s.blobs.Open(msg.BlobPath)
	if err != nil {
		s.log.Error("open message blob failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	content, extractErr := ingest.ExtractMessageContent(rc, maxDetailTextBytes)
	_ = rc.Close()
	if extractErr != nil {
		s.log.Error("extract message content failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
			"error", extractErr,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	var (
		renderURL           *string
		remoteResourceCount int
		rendered            render.Result
	)
	if msg.HasHTML {
		u := fmt.Sprintf("/render/%s/%s", pctx.Project.Slug, msg.ID)
		renderURL = &u
		if s.renderer != nil {
			res, renderErr := s.renderer.Render(r.Context(), pctx.Project.ID, msg.ID, msg.BlobPath)
			if renderErr != nil && !errors.Is(renderErr, render.ErrNoHTML) {
				s.log.Warn("compute remote resource count failed",
					"request_id", RequestIDFromContext(r.Context()),
					"project_id", pctx.Project.ID,
					"message_id", msg.ID,
					"error", renderErr,
				)
			} else if renderErr == nil {
				remoteResourceCount = res.RemoteResourceCount
				rendered = res
			}
		}
	}

	attResp := make([]attachmentResponse, 0, len(atts))
	for _, a := range atts {
		// FR-072: flag parts the /render rewrite inlines via cid:, using the
		// renderer's own resolution so the UI never parses email HTML.
		referenced := a.ContentID != nil && rendered.ReferencesCID(*a.ContentID)
		attResp = append(attResp, attachmentResponse{
			ID:               a.ID,
			Filename:         a.Filename,
			ContentType:      a.ContentType,
			SizeBytes:        a.SizeBytes,
			ContentID:        a.ContentID,
			Disposition:      a.Disposition,
			ReferencedByHTML: referenced,
		})
	}

	tags, err := s.store.ListTagsByMessagePK(r.Context(), pctx.Project.ID, msg.PK)
	if err != nil {
		s.log.Error("list tags for message detail failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	credLabel, err := s.credentialLabelForMessage(r.Context(), pctx.Project.ID, msg.SMTPCredentialID)
	if err != nil {
		s.log.Error("lookup credential label for message detail failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	resp := messageDetailResponse{
		messageSummaryResponse: toMessageSummary(msg, tags, credLabel),
		ReplyTo:                toAddressResponses(msg.ReplyTo),
		Bcc:                    toAddressResponses(msg.Bcc),
		MessageIDHeader:        msg.MessageIDHeader,
		DateHeader:             formatTimestampPtr(msg.DateHeader),
		Text:                   content.Text,
		TextGenerated:          msg.TextGenerated,
		TextTruncated:          content.TextTruncated,
		Attachments:            attResp,
		RenderURL:              renderURL,
		RemoteResourceCount:    remoteResourceCount,
	}

	// FR-071 & FR-066: omit envelope, parse_warnings, and headers for viewers when viewer_hide_technical is enabled.
	if pctx.CanReadTechnical() {
		warnings := msg.ParseWarnings
		if warnings == nil {
			warnings = []string{}
		}
		headers := content.Headers
		if headers == nil {
			headers = []ingest.Header{}
		}
		rcptTo := msg.EnvelopeTo
		if rcptTo == nil {
			rcptTo = []string{}
		}
		// FR-050: viewers (and read-only tokens) must never see SMTP usernames.
		var authUser *string
		if auth.Allowed(pctx.AuthRole, auth.ActionReadSMTP, pctx.AuthSettings()) {
			authUser = msg.AuthUser
		}
		resp.Envelope = &messageEnvelopeResponse{
			MailFrom: msg.EnvelopeFrom,
			RcptTo:   rcptTo,
			RemoteIP: msg.RemoteIP,
			HELO:     msg.HELO,
			TLS:      msg.TLS,
			AuthUser: authUser,
		}
		resp.ParseWarnings = &warnings
		resp.Headers = &headers
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetMessageRaw(w http.ResponseWriter, r *http.Request) {
	_, msg, ok := s.loadProjectMessage(w, r)
	if !ok {
		return
	}

	rc, err := s.blobs.Open(msg.BlobPath)
	if err != nil {
		s.log.Error("open message blob for raw failed",
			"request_id", RequestIDFromContext(r.Context()),
			"message_id", msg.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	defer func() { _ = rc.Close() }()

	liftWriteDeadline(w)
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", attachmentCSP)
	h.Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func (s *Server) handleGetMessageEML(w http.ResponseWriter, r *http.Request) {
	_, msg, ok := s.loadProjectMessage(w, r)
	if !ok {
		return
	}

	rc, err := s.blobs.Open(msg.BlobPath)
	if err != nil {
		s.log.Error("open message blob for eml failed",
			"request_id", RequestIDFromContext(r.Context()),
			"message_id", msg.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	defer func() { _ = rc.Close() }()

	filename := buildEMLFilename(msg.Subject, msg.ID)
	liftWriteDeadline(w)
	h := w.Header()
	h.Set("Content-Type", "message/rfc822")
	h.Set("Content-Disposition", formatContentDisposition("attachment", filename))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", attachmentCSP)
	h.Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func (s *Server) handleGetMessageHTML(w http.ResponseWriter, r *http.Request) {
	_, msg, ok := s.loadProjectMessage(w, r)
	if !ok {
		return
	}
	if !msg.HasHTML {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "message has no HTML part", nil)
		return
	}

	rc, err := s.blobs.Open(msg.BlobPath)
	if err != nil {
		s.log.Error("open message blob for html failed",
			"request_id", RequestIDFromContext(r.Context()),
			"message_id", msg.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	htmlSrc, extractErr := ingest.ExtractHTML(rc)
	_ = rc.Close()
	if extractErr != nil {
		s.log.Error("extract html failed",
			"request_id", RequestIDFromContext(r.Context()),
			"message_id", msg.ID,
			"error", extractErr,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", attachmentCSP)
	h.Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, htmlSrc)
}

func (s *Server) handleGetMessageHTMLCheck(w http.ResponseWriter, r *http.Request) {
	pctx, msg, ok := s.loadProjectMessage(w, r)
	if !ok {
		return
	}
	if !msg.HasHTML {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "message has no HTML part", nil)
		return
	}
	if s.compatChecker == nil {
		s.log.Error("html compatibility checker is not initialized",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	report, err := s.compatChecker.Check(r.Context(), pctx.Project.ID, msg.ID, msg.BlobPath)
	if err != nil {
		if errors.Is(err, compat.ErrNoHTML) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "message has no HTML part", nil)
			return
		}
		if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
			return
		}
		s.log.Error("html compatibility check failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleGetMessageHeaders(w http.ResponseWriter, r *http.Request) {
	_, msg, ok := s.loadProjectMessage(w, r)
	if !ok {
		return
	}

	rc, err := s.blobs.Open(msg.BlobPath)
	if err != nil {
		s.log.Error("open message blob for headers failed",
			"request_id", RequestIDFromContext(r.Context()),
			"message_id", msg.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	headers, parseErr := ingest.ParseRawHeaders(rc)
	_ = rc.Close()
	if parseErr != nil {
		s.log.Error("parse raw headers failed",
			"request_id", RequestIDFromContext(r.Context()),
			"message_id", msg.ID,
			"error", parseErr,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	if headers == nil {
		headers = []ingest.Header{}
	}
	writeJSON(w, http.StatusOK, headers)
}

func (s *Server) handleGetMessageAttachment(w http.ResponseWriter, r *http.Request) {
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
		s.log.Error("get attachment metadata failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
			"attachment_id", attID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	rc, err := s.blobs.Open(msg.BlobPath)
	if err != nil {
		s.log.Error("open message blob for attachment failed",
			"request_id", RequestIDFromContext(r.Context()),
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
		s.log.Error("extract attachment failed",
			"request_id", RequestIDFromContext(r.Context()),
			"message_id", msg.ID,
			"attachment_id", attID,
			"error", extractErr,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	safeCT := safeAttachmentContentType(att.ContentType)
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		if _, allowed := inlineRasterImageTypes[safeCT]; allowed {
			disposition = "inline"
		}
	}

	filename := strings.TrimSpace(att.Filename)
	if filename == "" {
		filename = fmt.Sprintf("attachment-%d", att.ID)
	}

	liftWriteDeadline(w)
	h := w.Header()
	h.Set("Content-Type", safeCT)
	h.Set("Content-Disposition", formatContentDisposition(disposition, filename))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", attachmentCSP)
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) loadProjectMessage(w http.ResponseWriter, r *http.Request) (ProjectContext, store.Message, bool) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return ProjectContext{}, store.Message{}, false
	}
	msgID := r.PathValue("id")
	if msgID == "" {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "message not found", nil)
		return ProjectContext{}, store.Message{}, false
	}

	msg, err := s.store.GetMessageByID(r.Context(), pctx.Project.ID, msgID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "message not found", nil)
			return ProjectContext{}, store.Message{}, false
		}
		s.log.Error("get message failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msgID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return ProjectContext{}, store.Message{}, false
	}
	return pctx, msg, true
}

func (s *Server) credentialLabelsForMessages(ctx context.Context, projectID int64, msgs []store.Message) (map[int64]string, error) {
	hasAny := false
	for _, m := range msgs {
		if m.SMTPCredentialID != nil {
			hasAny = true
			break
		}
	}
	if !hasAny {
		return nil, nil
	}
	creds, err := s.store.ListSMTPCredentialsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(creds))
	for _, c := range creds {
		out[c.ID] = c.Label
	}
	return out, nil
}

func (s *Server) credentialLabelForMessage(ctx context.Context, projectID int64, credID *int64) (string, error) {
	if credID == nil {
		return "", nil
	}
	c, err := s.store.GetSMTPCredentialByID(ctx, projectID, *credID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return c.Label, nil
}

func toMessageSummary(m store.Message, tags []string, credentialLabel string) messageSummaryResponse {
	if tags == nil {
		tags = []string{}
	}
	return messageSummaryResponse{
		ID:         m.ID,
		ReceivedAt: Timestamp(m.ReceivedAt),
		From: addressResponse{
			Name:    m.FromName,
			Address: m.FromAddr,
		},
		To:              toAddressResponses(m.To),
		Cc:              toAddressResponses(m.Cc),
		Subject:         m.Subject,
		Snippet:         m.Snippet,
		SizeBytes:       m.SizeBytes,
		Seen:            m.Seen,
		AttachmentCount: m.AttachmentCount,
		HasHTML:         m.HasHTML,
		HasText:         m.HasText,
		Tags:            tags,
		CredentialLabel: credentialLabel,
	}
}

func toAddressResponses(addrs []store.Address) []addressResponse {
	out := make([]addressResponse, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, addressResponse{
			Name:    a.Name,
			Address: a.Address,
		})
	}
	return out
}

func safeAttachmentContentType(declared string) string {
	for _, r := range declared {
		if unicode.IsControl(r) {
			return defaultAttachmentContentType
		}
	}
	mediaType, _, err := mime.ParseMediaType(declared)
	if err != nil {
		return defaultAttachmentContentType
	}
	typ, subtype, ok := strings.Cut(mediaType, "/")
	if !ok || typ == "" || subtype == "" || strings.Contains(subtype, "/") {
		return defaultAttachmentContentType
	}
	if typ == "multipart" {
		return defaultAttachmentContentType
	}
	if subtype == "xml" || strings.HasSuffix(subtype, "+xml") {
		return defaultAttachmentContentType
	}
	if _, unsafe := activeContentTypes[mediaType]; unsafe {
		return defaultAttachmentContentType
	}
	return mediaType
}

func buildEMLFilename(subject, msgID string) string {
	var b strings.Builder
	lastDash := true
	count := 0
	for _, r := range strings.TrimSpace(subject) {
		if count >= maxEMLFilenameRunes {
			break
		}
		if unicode.IsControl(r) || r == '/' || r == '\\' || r == '"' || r == ':' || r == '*' || r == '?' || r == '<' || r == '>' || r == '|' {
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
				count++
			}
			continue
		}
		if unicode.IsSpace(r) {
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
				count++
			}
			continue
		}
		b.WriteRune(r)
		lastDash = false
		count++
	}
	base := strings.Trim(b.String(), "-. ")
	if base == "" {
		base = msgID
	}
	return base + ".eml"
}

func formatContentDisposition(disposition, filename string) string {
	clean := sanitizeHeaderFilename(filename)
	ascii := asciiFallbackFilename(clean)
	encoded := rfc5987Encode(clean)
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disposition, ascii, encoded)
}

func sanitizeHeaderFilename(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return defaultAttachmentExt
	}
	return out
}

func asciiFallbackFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 0x20 && r <= 0x7E && r != '"' && r != '\\' && r != ';' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return defaultAttachmentExt
	}
	return out
}

func rfc5987Encode(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isRFC5987AttrChar(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0F])
		}
	}
	return b.String()
}

func isRFC5987AttrChar(c byte) bool {
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
		return true
	}
	switch c {
	case '!', '#', '$', '&', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

func liftWriteDeadline(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
}
