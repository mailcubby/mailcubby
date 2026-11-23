package api

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/store"
)

const (
	defaultProjectColor = "slate"
	maxProjectNameRunes = 80
)

// validProjectColors lists the 12 preset colour tokens defined by SPEC FR-030 and UI.md.
var validProjectColors = map[string]struct{}{
	"slate":   {},
	"red":     {},
	"orange":  {},
	"amber":   {},
	"lime":    {},
	"green":   {},
	"teal":    {},
	"cyan":    {},
	"blue":    {},
	"violet":  {},
	"fuchsia": {},
	"pink":    {},
}

type projectSummaryResponse struct {
	ID            int64      `json:"id"`
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	Color         string     `json:"color"`
	Role          string     `json:"role"`
	MessageCount  int64      `json:"message_count"`
	UnreadCount   int64      `json:"unread_count"`
	SizeBytes     int64      `json:"size_bytes"`
	LastMessageAt *Timestamp `json:"last_message_at"`
	Sparkline     []int64    `json:"sparkline"`
}

type projectSettingsResponse struct {
	RetentionMaxAge         *int64 `json:"retention_max_age"`
	RetentionMaxMessages    *int64 `json:"retention_max_messages"`
	RetentionMaxBytes       *int64 `json:"retention_max_bytes"`
	RecipientRoutingEnabled bool   `json:"recipient_routing_enabled"`
	PlusAddressTags         bool   `json:"plus_address_tags"`
	ViewerHideTechnical     bool   `json:"viewer_hide_technical"`
	MembersCanRevealSMTP    bool   `json:"members_can_reveal_smtp"`
}

type projectDetailResponse struct {
	ID            int64                   `json:"id"`
	Slug          string                  `json:"slug"`
	Name          string                  `json:"name"`
	Color         string                  `json:"color"`
	Role          string                  `json:"role"`
	MessageCount  int64                   `json:"message_count"`
	UnreadCount   int64                   `json:"unread_count"`
	SizeBytes     int64                   `json:"size_bytes"`
	LastMessageAt *Timestamp              `json:"last_message_at"`
	CreatedAt     Timestamp               `json:"created_at"`
	UpdatedAt     Timestamp               `json:"updated_at"`
	Settings      projectSettingsResponse `json:"settings"`
}

type createProjectRequest struct {
	Name  string  `json:"name"`
	Slug  *string `json:"slug"`
	Color *string `json:"color"`
}

type patchProjectSettingsRequest struct {
	RetentionMaxAge         Nullable[int64] `json:"retention_max_age"`
	RetentionMaxMessages    Nullable[int64] `json:"retention_max_messages"`
	RetentionMaxBytes       Nullable[int64] `json:"retention_max_bytes"`
	RecipientRoutingEnabled *bool           `json:"recipient_routing_enabled"`
	PlusAddressTags         *bool           `json:"plus_address_tags"`
	ViewerHideTechnical     *bool           `json:"viewer_hide_technical"`
	MembersCanRevealSMTP    *bool           `json:"members_can_reveal_smtp"`
}

type patchProjectRequest struct {
	Name     *string                      `json:"name"`
	Slug     *string                      `json:"slug"`
	Color    *string                      `json:"color"`
	Settings *patchProjectSettingsRequest `json:"settings"`
}

type deleteProjectRequest struct {
	ConfirmSlug string `json:"confirm_slug"`
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		s.log.Error("list projects failed",
			"request_id", RequestIDFromContext(r.Context()),
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	out := make([]projectSummaryResponse, 0, len(projects))
	for _, p := range projects {
		spark, err := s.getSparkline(r.Context(), p.ID)
		if err != nil {
			s.log.Error("compute project sparkline failed",
				"request_id", RequestIDFromContext(r.Context()),
				"project_id", p.ID,
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
		sparkSlice := make([]int64, len(spark))
		copy(sparkSlice, spark[:])
		out = append(out, projectSummaryResponse{
			ID:            p.ID,
			Slug:          p.Slug,
			Name:          p.Name,
			Color:         p.Color,
			Role:          "owner",
			MessageCount:  p.MessageCount,
			UnreadCount:   p.UnreadCount,
			SizeBytes:     p.SizeBytes,
			LastMessageAt: formatTimestampPtr(p.LastMessageAt),
			Sparkline:     sparkSlice,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	details := make(map[string]string)
	name := strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxProjectNameRunes {
		details["name"] = "must be between 1 and 80 characters"
	}

	var slug string
	if req.Slug != nil && strings.TrimSpace(*req.Slug) != "" {
		slug = strings.TrimSpace(*req.Slug)
		if err := config.ValidateSlug(slug); err != nil {
			details["slug"] = err.Error()
		}
	} else if name != "" {
		slug = config.Slugify(name, s.cfg.DefaultProject)
	}

	color := defaultProjectColor
	if req.Color != nil && strings.TrimSpace(*req.Color) != "" {
		color = strings.TrimSpace(*req.Color)
		if _, ok := validProjectColors[color]; !ok {
			details["color"] = "must be one of the 12 preset color tokens"
		}
	}

	if len(details) > 0 {
		writeValidationError(w, details)
		return
	}

	proj, err := s.store.CreateProject(r.Context(), store.CreateProjectParams{
		Slug:     slug,
		Name:     name,
		Color:    color,
		Settings: store.DefaultProjectSettings(),
		Now:      s.clk.Now().UTC(),
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "a project with this slug already exists", nil)
			return
		}
		s.log.Error("create project failed",
			"request_id", RequestIDFromContext(r.Context()),
			"slug", slug,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:      events.TypeProjectCreated,
			ProjectID: proj.ID,
			Payload: events.ProjectCreatedPayload{
				Project: proj.Slug,
			},
		})
	}

	writeJSON(w, http.StatusCreated, buildProjectDetail(proj, "owner"))
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, buildProjectDetail(pctx.Project, pctx.Role))
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	var req patchProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	details := make(map[string]string)
	nextName := pctx.Project.Name
	nextSlug := pctx.Project.Slug
	nextColor := pctx.Project.Color
	nextSettings := pctx.Project.Settings

	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if n := utf8.RuneCountInString(trimmed); n < 1 || n > maxProjectNameRunes {
			details["name"] = "must be between 1 and 80 characters"
		} else {
			nextName = trimmed
		}
	}

	if req.Slug != nil {
		trimmed := strings.TrimSpace(*req.Slug)
		if err := config.ValidateSlug(trimmed); err != nil {
			details["slug"] = err.Error()
		} else {
			nextSlug = trimmed
		}
	}

	if req.Color != nil {
		trimmed := strings.TrimSpace(*req.Color)
		if _, valid := validProjectColors[trimmed]; !valid {
			details["color"] = "must be one of the 12 preset color tokens"
		} else {
			nextColor = trimmed
		}
	}

	if req.Settings != nil {
		st := req.Settings
		if st.RetentionMaxAge.Set {
			if st.RetentionMaxAge.Value != nil && *st.RetentionMaxAge.Value < 0 {
				details["settings.retention_max_age"] = "must be greater than or equal to 0"
			} else {
				nextSettings.RetentionMaxAgeSec = st.RetentionMaxAge.Value
			}
		}
		if st.RetentionMaxMessages.Set {
			if st.RetentionMaxMessages.Value != nil && *st.RetentionMaxMessages.Value < 0 {
				details["settings.retention_max_messages"] = "must be greater than or equal to 0"
			} else {
				nextSettings.RetentionMaxMessages = st.RetentionMaxMessages.Value
			}
		}
		if st.RetentionMaxBytes.Set {
			if st.RetentionMaxBytes.Value != nil && *st.RetentionMaxBytes.Value < 0 {
				details["settings.retention_max_bytes"] = "must be greater than or equal to 0"
			} else {
				nextSettings.RetentionMaxBytes = st.RetentionMaxBytes.Value
			}
		}
		if st.RecipientRoutingEnabled != nil {
			nextSettings.RecipientRoutingEnabled = *st.RecipientRoutingEnabled
		}
		if st.PlusAddressTags != nil {
			nextSettings.PlusAddressTags = *st.PlusAddressTags
		}
		if st.ViewerHideTechnical != nil {
			nextSettings.ViewerHideTechnical = *st.ViewerHideTechnical
		}
		if st.MembersCanRevealSMTP != nil {
			nextSettings.MembersCanRevealSMTP = *st.MembersCanRevealSMTP
		}
	}

	if len(details) > 0 {
		writeValidationError(w, details)
		return
	}

	updated, err := s.store.UpdateProject(r.Context(), store.UpdateProjectParams{
		ID:       pctx.Project.ID,
		Slug:     nextSlug,
		Name:     nextName,
		Color:    nextColor,
		Settings: nextSettings,
		Now:      s.clk.Now().UTC(),
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "a project with this slug already exists", nil)
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
			return
		}
		s.log.Error("update project failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:      events.TypeProjectUpdated,
			ProjectID: updated.ID,
			Payload: events.ProjectUpdatedPayload{
				Project:       updated.Slug,
				MessageCount:  updated.MessageCount,
				UnreadCount:   updated.UnreadCount,
				SizeBytes:     updated.SizeBytes,
				LastMessageAt: updated.LastMessageAt,
			},
		})
	}

	writeJSON(w, http.StatusOK, buildProjectDetail(updated, pctx.Role))
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	var req deleteProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.ConfirmSlug != pctx.Project.Slug {
		writeValidationError(w, map[string]string{
			"confirm_slug": "must match the project slug",
		})
		return
	}

	if err := s.store.DeleteProject(r.Context(), pctx.Project.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
			return
		}
		s.log.Error("delete project failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	// TODO(phase-10): delegate project blob directory removal to OrphanGC worker.
	if s.blobs != nil {
		if err := s.blobs.RemoveProject(pctx.Project.ID); err != nil {
			s.log.Warn("best-effort project blob cleanup failed",
				"request_id", RequestIDFromContext(r.Context()),
				"project_id", pctx.Project.ID,
				"error", err,
			)
		}
	}

	s.invalidateSparkline(pctx.Project.ID)
	if s.bus != nil {
		s.bus.Publish(events.Event{
			Type:      events.TypeProjectDeleted,
			ProjectID: pctx.Project.ID,
			Payload: events.ProjectDeletedPayload{
				Project: pctx.Project.Slug,
			},
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

func buildProjectDetail(p store.Project, role string) projectDetailResponse {
	return projectDetailResponse{
		ID:            p.ID,
		Slug:          p.Slug,
		Name:          p.Name,
		Color:         p.Color,
		Role:          role,
		MessageCount:  p.MessageCount,
		UnreadCount:   p.UnreadCount,
		SizeBytes:     p.SizeBytes,
		LastMessageAt: formatTimestampPtr(p.LastMessageAt),
		CreatedAt:     Timestamp(p.CreatedAt),
		UpdatedAt:     Timestamp(p.UpdatedAt),
		Settings: projectSettingsResponse{
			RetentionMaxAge:         p.Settings.RetentionMaxAgeSec,
			RetentionMaxMessages:    p.Settings.RetentionMaxMessages,
			RetentionMaxBytes:       p.Settings.RetentionMaxBytes,
			RecipientRoutingEnabled: p.Settings.RecipientRoutingEnabled,
			PlusAddressTags:         p.Settings.PlusAddressTags,
			ViewerHideTechnical:     p.Settings.ViewerHideTechnical,
			MembersCanRevealSMTP:    p.Settings.MembersCanRevealSMTP,
		},
	}
}
