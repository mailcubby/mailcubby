package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mailcubby/mailcubby/internal/store"
)

type auditEntryResponse struct {
	ID          int64          `json:"id"`
	At          Timestamp      `json:"at"`
	ActorType   string         `json:"actor_type"`
	ActorUserID *int64         `json:"actor_user_id"`
	ActorLabel  *string        `json:"actor_label"`
	ProjectID   *int64         `json:"project_id"`
	ProjectSlug *string        `json:"project_slug"`
	Action      string         `json:"action"`
	TargetType  *string        `json:"target_type"`
	TargetID    *string        `json:"target_id"`
	IP          *string        `json:"ip"`
	Metadata    map[string]any `json:"metadata"`
}

type auditListResponse struct {
	Items      []auditEntryResponse `json:"items"`
	NextCursor *string              `json:"next_cursor"`
}

func (s *Server) handleListProjectAudit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	limit, beforeID, ok := parseAuditPagination(w, r)
	if !ok {
		return
	}
	action := strings.TrimSpace(r.URL.Query().Get("action"))

	entries, err := s.store.ListProjectAudit(ctx, store.ListProjectAuditParams{
		ProjectID: pctx.Project.ID,
		BeforeID:  beforeID,
		Action:    action,
		Limit:     limit + 1,
	})
	if err != nil {
		s.log.Error("list project audit failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	writeJSON(w, http.StatusOK, buildAuditListResponse(entries, limit))
}

func (s *Server) handleAdminListAudit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, beforeID, ok := parseAuditPagination(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	action := strings.TrimSpace(q.Get("action"))
	rawProject := strings.TrimSpace(q.Get("project"))
	rawUser := strings.TrimSpace(q.Get("user"))

	var projectIDFilter int64
	if rawProject != "" {
		proj, err := s.store.GetProjectBySlug(ctx, rawProject)
		switch {
		case err == nil:
			projectIDFilter = proj.ID
		case errors.Is(err, store.ErrNotFound):
			if parsedID, parseErr := parsePositiveInt64Param(rawProject); parseErr == nil {
				projectIDFilter = parsedID
			} else {
				// Nonexistent project slug yields an empty result set.
				writeJSON(w, http.StatusOK, auditListResponse{Items: []auditEntryResponse{}, NextCursor: nil})
				return
			}
		default:
			s.log.Error("admin audit resolve project failed",
				"request_id", RequestIDFromContext(ctx),
				"project", rawProject,
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
	}

	var (
		userIDFilter    int64
		userLabelFilter string
	)
	if rawUser != "" {
		if parsedID, err := parsePositiveInt64Param(rawUser); err == nil {
			userIDFilter = parsedID
		} else {
			userLabelFilter = rawUser
		}
	}

	entries, err := s.store.ListAdminAudit(ctx, store.ListAdminAuditParams{
		BeforeID:  beforeID,
		Action:    action,
		ProjectID: projectIDFilter,
		UserID:    userIDFilter,
		UserLabel: userLabelFilter,
		Limit:     limit + 1,
	})
	if err != nil {
		s.log.Error("admin list audit failed",
			"request_id", RequestIDFromContext(ctx),
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	writeJSON(w, http.StatusOK, buildAuditListResponse(entries, limit))
}

func parseAuditPagination(w http.ResponseWriter, r *http.Request) (limit, beforeID int64, ok bool) {
	q := r.URL.Query()
	limit = int64(defaultPageLimit)
	if rawLimit := strings.TrimSpace(q.Get("limit")); rawLimit != "" {
		parsed, err := strconv.ParseInt(rawLimit, 10, 64)
		if err != nil || parsed < 1 || parsed > maxPageLimit {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "limit must be an integer between 1 and 200", nil)
			return 0, 0, false
		}
		limit = parsed
	}
	if rawCursor := strings.TrimSpace(q.Get("cursor")); rawCursor != "" {
		decoded, err := decodeIntCursor(rawCursor)
		if err != nil || decoded <= 0 {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid cursor parameter", nil)
			return 0, 0, false
		}
		beforeID = decoded
	}
	return limit, beforeID, true
}

func buildAuditListResponse(entries []store.AuditEntry, limit int64) auditListResponse {
	var nextCursor *string
	if int64(len(entries)) > limit {
		entries = entries[:limit]
		cur := encodeIntCursor(entries[len(entries)-1].ID)
		nextCursor = &cur
	}
	items := make([]auditEntryResponse, 0, len(entries))
	for _, e := range entries {
		var meta map[string]any
		if e.MetadataJSON != nil && strings.TrimSpace(*e.MetadataJSON) != "" {
			_ = json.Unmarshal([]byte(*e.MetadataJSON), &meta)
		}
		items = append(items, auditEntryResponse{
			ID:          e.ID,
			At:          Timestamp(e.At),
			ActorType:   string(e.ActorType),
			ActorUserID: e.ActorUserID,
			ActorLabel:  e.ActorLabel,
			ProjectID:   e.ProjectID,
			ProjectSlug: e.ProjectSlug,
			Action:      e.Action,
			TargetType:  e.TargetType,
			TargetID:    e.TargetID,
			IP:          e.IP,
			Metadata:    meta,
		})
	}
	return auditListResponse{
		Items:      items,
		NextCursor: nextCursor,
	}
}
