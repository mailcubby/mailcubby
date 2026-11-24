package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/store"
)

var (
	errLastProjectOwner = errors.New("cannot remove or demote the last project owner")
	errNotProjectMember = errors.New("caller is not an explicit member of this project")
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

type nonOwnerProjectSettingsResponse struct {
	PlusAddressTags     bool `json:"plus_address_tags"`
	ViewerHideTechnical bool `json:"viewer_hide_technical"`
}

type projectDetailResponse struct {
	ID            int64      `json:"id"`
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	Color         string     `json:"color"`
	Role          string     `json:"role"`
	MessageCount  int64      `json:"message_count"`
	UnreadCount   int64      `json:"unread_count"`
	SizeBytes     int64      `json:"size_bytes"`
	LastMessageAt *Timestamp `json:"last_message_at"`
	CreatedAt     Timestamp  `json:"created_at"`
	UpdatedAt     Timestamp  `json:"updated_at"`
	Settings      any        `json:"settings"`
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
	principal := PrincipalFromContext(r.Context())

	var list []store.ProjectWithRole
	if s.cfg.Mode == config.ModeLocal || principal.IsAdmin {
		projects, err := s.store.ListProjects(r.Context())
		if err != nil {
			s.log.Error("list projects failed",
				"request_id", RequestIDFromContext(r.Context()),
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
		list = make([]store.ProjectWithRole, 0, len(projects))
		for _, p := range projects {
			list = append(list, store.ProjectWithRole{
				Project: p,
				Role:    string(auth.RoleOwner),
			})
		}
	} else {
		var err error
		list, err = s.store.ListProjectsForUser(r.Context(), principal.UserID)
		if err != nil {
			s.log.Error("list projects for user failed",
				"request_id", RequestIDFromContext(r.Context()),
				"user_id", principal.UserID,
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
	}

	out := make([]projectSummaryResponse, 0, len(list))
	for _, item := range list {
		p := item.Project
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
			Role:          item.Role,
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

	ctx := r.Context()
	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	var proj store.Project
	err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		created, err := tx.CreateProject(ctx, store.CreateProjectParams{
			Slug:     slug,
			Name:     name,
			Color:    color,
			Settings: store.DefaultProjectSettings(),
			Now:      now,
		})
		if err != nil {
			return err
		}
		if s.cfg.Mode == config.ModeServer && principal.Kind == auth.PrincipalUser {
			// FR-031: creator becomes owner of the new project.
			if _, err := tx.AddProjectMember(ctx, created.ID, principal.UserID, string(auth.RoleOwner), now); err != nil {
				return err
			}
		}
		targetType := "project"
		targetID := strconv.FormatInt(created.ID, 10)
		if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			ProjectID:   &created.ID,
			Action:      "project.create",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata:    map[string]any{"slug": created.Slug, "name": created.Name},
		}); err != nil {
			return err
		}
		proj = created
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "a project with this slug already exists", nil)
			return
		}
		s.log.Error("create project failed",
			"request_id", RequestIDFromContext(ctx),
			"slug", slug,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	if s.cfg.Mode == config.ModeServer && principal.Kind == auth.PrincipalUser {
		s.publishMembershipChanged(principal.UserID, proj.Slug)
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

	writeJSON(w, http.StatusCreated, buildProjectDetail(proj, string(auth.RoleOwner), true))
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}
	isOwner := pctx.AuthRole == auth.RoleOwner || pctx.AuthRole == auth.RoleAdmin
	writeJSON(w, http.StatusOK, buildProjectDetail(pctx.Project, pctx.Role, isOwner))
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

	ctx := r.Context()
	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	var updated store.Project
	err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		u, err := tx.UpdateProject(ctx, store.UpdateProjectParams{
			ID:       pctx.Project.ID,
			Slug:     nextSlug,
			Name:     nextName,
			Color:    nextColor,
			Settings: nextSettings,
			Now:      now,
		})
		if err != nil {
			return err
		}
		targetType := "project"
		targetID := strconv.FormatInt(u.ID, 10)
		if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			ProjectID:   &u.ID,
			Action:      "project.update",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata:    map[string]any{"slug": u.Slug},
		}); err != nil {
			return err
		}
		updated = u
		return nil
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
			"request_id", RequestIDFromContext(ctx),
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

	writeJSON(w, http.StatusOK, buildProjectDetail(updated, pctx.Role, true))
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

	ctx := r.Context()
	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.DeleteProject(ctx, pctx.Project.ID); err != nil {
			return err
		}
		targetType := "project"
		targetID := strconv.FormatInt(pctx.Project.ID, 10)
		_, err := tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			ProjectID:   nil, // Project row already deleted; preserve ID/slug in target & metadata.
			Action:      "project.delete",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata:    map[string]any{"slug": pctx.Project.Slug, "name": pctx.Project.Name},
		})
		return err
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
			return
		}
		s.log.Error("delete project failed",
			"request_id", RequestIDFromContext(ctx),
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
				"request_id", RequestIDFromContext(ctx),
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

func (s *Server) handleLeaveProject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}
	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		member, err := tx.GetProjectMember(ctx, pctx.Project.ID, principal.UserID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return errNotProjectMember
			}
			return err
		}
		if member.Role == string(auth.RoleOwner) {
			owners, err := tx.CountProjectOwners(ctx, pctx.Project.ID)
			if err != nil {
				return err
			}
			if owners <= 1 {
				return errLastProjectOwner
			}
		}
		if err := tx.DeleteProjectMember(ctx, pctx.Project.ID, principal.UserID); err != nil {
			return err
		}
		targetType := "user"
		targetID := strconv.FormatInt(principal.UserID, 10)
		_, err = tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			ProjectID:   &pctx.Project.ID,
			Action:      "member.remove",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata:    map[string]any{"self": true, "role": member.Role},
		})
		return err
	})
	if err != nil {
		if errors.Is(err, errNotProjectMember) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "you are not a member of this project", nil)
			return
		}
		if errors.Is(err, errLastProjectOwner) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "cannot leave project as the last owner", nil)
			return
		}
		s.log.Error("leave project failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"user_id", principal.UserID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	s.publishMembershipChanged(principal.UserID, pctx.Project.Slug)
	w.WriteHeader(http.StatusNoContent)
}

func buildProjectDetail(p store.Project, role string, isOwner bool) projectDetailResponse {
	var settings any
	if isOwner {
		settings = projectSettingsResponse{
			RetentionMaxAge:         p.Settings.RetentionMaxAgeSec,
			RetentionMaxMessages:    p.Settings.RetentionMaxMessages,
			RetentionMaxBytes:       p.Settings.RetentionMaxBytes,
			RecipientRoutingEnabled: p.Settings.RecipientRoutingEnabled,
			PlusAddressTags:         p.Settings.PlusAddressTags,
			ViewerHideTechnical:     p.Settings.ViewerHideTechnical,
			MembersCanRevealSMTP:    p.Settings.MembersCanRevealSMTP,
		}
	} else {
		settings = nonOwnerProjectSettingsResponse{
			PlusAddressTags:     p.Settings.PlusAddressTags,
			ViewerHideTechnical: p.Settings.ViewerHideTechnical,
		}
	}
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
		Settings:      settings,
	}
}
