package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mailcubby/mailcubby/internal/store"
)

const maxTagsPerMessageOp = 50

type tagResponse struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Color        string `json:"color"`
	MessageCount int64  `json:"message_count"`
}

type createTagRequest struct {
	Name  string  `json:"name"`
	Color *string `json:"color"`
}

type patchTagRequest struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	tags, err := s.store.ListTags(r.Context(), pctx.Project.ID)
	if err != nil {
		s.log.Error("list tags failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	out := make([]tagResponse, 0, len(tags))
	for _, t := range tags {
		out = append(out, toTagResponse(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateTag(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	var req createTagRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	details := make(map[string]string)
	name := strings.TrimSpace(req.Name)
	if err := store.ValidateTagName(name); err != nil {
		details["name"] = err.Error()
	}

	color := store.DefaultTagColor
	if req.Color != nil {
		c := strings.TrimSpace(*req.Color)
		if _, valid := validProjectColors[c]; !valid {
			details["color"] = "must be one of the 12 preset color tokens"
		} else {
			color = c
		}
	}

	if len(details) > 0 {
		writeValidationError(w, details)
		return
	}

	tag, err := s.store.CreateTag(r.Context(), pctx.Project.ID, name, color, s.clk.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "a tag with this name already exists in the project", nil)
			return
		}
		s.log.Error("create tag failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	writeJSON(w, http.StatusCreated, toTagResponse(tag))
}

func (s *Server) handleUpdateTag(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	tagID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || tagID <= 0 {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid tag id", nil)
		return
	}

	var req patchTagRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	details := make(map[string]string)
	var (
		nextName  *string
		nextColor *string
	)
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if err := store.ValidateTagName(trimmed); err != nil {
			details["name"] = err.Error()
		} else {
			nextName = &trimmed
		}
	}
	if req.Color != nil {
		trimmed := strings.TrimSpace(*req.Color)
		if _, valid := validProjectColors[trimmed]; !valid {
			details["color"] = "must be one of the 12 preset color tokens"
		} else {
			nextColor = &trimmed
		}
	}
	if len(details) > 0 {
		writeValidationError(w, details)
		return
	}

	tag, msgUpdates, err := s.store.UpdateTag(r.Context(), pctx.Project.ID, tagID, nextName, nextColor)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "tag not found", nil)
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "a tag with this name already exists in the project", nil)
			return
		}
		s.log.Error("update tag failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"tag_id", tagID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	for _, u := range msgUpdates {
		tags := u.Tags
		s.publishMessageUpdated(pctx.Project, []string{u.ID}, nil, &tags)
	}

	writeJSON(w, http.StatusOK, toTagResponse(tag))
}

func (s *Server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	tagID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || tagID <= 0 {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid tag id", nil)
		return
	}

	msgUpdates, err := s.store.DeleteTag(r.Context(), pctx.Project.ID, tagID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "tag not found", nil)
			return
		}
		s.log.Error("delete tag failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"tag_id", tagID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	for _, u := range msgUpdates {
		tags := u.Tags
		s.publishMessageUpdated(pctx.Project, []string{u.ID}, nil, &tags)
	}

	w.WriteHeader(http.StatusNoContent)
}

func toTagResponse(t store.Tag) tagResponse {
	return tagResponse{
		ID:           t.ID,
		Name:         t.Name,
		Color:        t.Color,
		MessageCount: t.MessageCount,
	}
}

func validateTagList(raw []string) ([]string, string) {
	if len(raw) > maxTagsPerMessageOp {
		return nil, "must contain at most 50 tags"
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		trimmed := strings.TrimSpace(item)
		if err := store.ValidateTagName(trimmed); err != nil {
			return nil, err.Error()
		}
		out = append(out, trimmed)
	}
	return out, ""
}
