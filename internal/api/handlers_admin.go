package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/retention"
	"github.com/mailcubby/mailcubby/internal/store"
)

var errLastAdmin = errors.New("cannot remove, demote, or disable the last admin")

type adminUserResponse struct {
	ID                int64      `json:"id"`
	Email             string     `json:"email"`
	Name              string     `json:"name"`
	IsAdmin           bool       `json:"is_admin"`
	CanCreateProjects bool       `json:"can_create_projects"`
	HasPassword       bool       `json:"has_password"`
	DisabledAt        *Timestamp `json:"disabled_at"`
	LastLoginAt       *Timestamp `json:"last_login_at"`
	CreatedAt         Timestamp  `json:"created_at"`
	ProjectCount      int64      `json:"project_count"`
}

type adminUserListResponse struct {
	Items      []adminUserResponse `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}

type adminCreateUserRequest struct {
	Email             string `json:"email"`
	Name              string `json:"name"`
	IsAdmin           bool   `json:"is_admin"`
	CanCreateProjects bool   `json:"can_create_projects"`
}

type adminCreateUserResponse struct {
	User      adminUserResponse `json:"user"`
	InviteURL string            `json:"invite_url"`
}

type adminUpdateUserRequest struct {
	Name              *string `json:"name"`
	IsAdmin           *bool   `json:"is_admin"`
	CanCreateProjects *bool   `json:"can_create_projects"`
	Disabled          *bool   `json:"disabled"`
}

type adminPasswordResetResponse struct {
	URL       string    `json:"url"`
	ExpiresAt Timestamp `json:"expires_at"`
}

type adminProjectResponse struct {
	ID            int64                `json:"id"`
	Slug          string               `json:"slug"`
	Name          string               `json:"name"`
	Color         string               `json:"color"`
	MessageCount  int64                `json:"message_count"`
	UnreadCount   int64                `json:"unread_count"`
	SizeBytes     int64                `json:"size_bytes"`
	LastMessageAt *Timestamp           `json:"last_message_at"`
	CreatedAt     Timestamp            `json:"created_at"`
	Owners        []memberUserResponse `json:"owners"`
}

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
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

	var afterID int64
	if rawCursor := strings.TrimSpace(q.Get("cursor")); rawCursor != "" {
		decoded, err := decodeIntCursor(rawCursor)
		if err != nil {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid cursor parameter", nil)
			return
		}
		afterID = decoded
	}

	searchQ := strings.TrimSpace(q.Get("q"))
	rows, err := s.store.ListUsersAdmin(ctx, afterID, searchQ, limit+1)
	if err != nil {
		s.log.Error("admin list users failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	var nextCursor *string
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		cur := encodeIntCursor(rows[len(rows)-1].User.ID)
		nextCursor = &cur
	}

	items := make([]adminUserResponse, 0, len(rows))
	for _, u := range rows {
		items = append(items, toAdminUserResponse(u))
	}

	writeJSON(w, http.StatusOK, adminUserListResponse{
		Items:      items,
		NextCursor: nextCursor,
	})
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req adminCreateUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	details := make(map[string]string)
	email, err := auth.ValidateEmail(req.Email)
	if err != nil {
		details["email"] = err.Error()
	}
	name := strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxUserNameRunes {
		details["name"] = "must be between 1 and 80 characters"
	}
	if len(details) > 0 {
		writeValidationError(w, details)
		return
	}

	rawToken, err := auth.GenerateToken()
	if err != nil {
		s.log.Error("admin create user generate invite token failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	expiresAt := now.Add(s.cfg.InviteTTL)
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	var created store.User
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		u, err := tx.CreateUser(ctx, store.CreateUserParams{
			Email:             email,
			Name:              name,
			PasswordHash:      nil,
			IsAdmin:           req.IsAdmin,
			CanCreateProjects: req.CanCreateProjects || req.IsAdmin,
			Now:               now,
		})
		if err != nil {
			return err
		}

		// FR-055 & FR-100: create an instance invite (project_id=NULL, role=NULL) for the new passwordless user.
		if _, err := tx.CreateInvite(ctx, store.CreateInviteParams{
			ProjectID: nil,
			Email:     u.Email,
			Role:      nil,
			TokenHash: auth.HashToken(rawToken),
			CreatedBy: actorUserID,
			Now:       now,
			ExpiresAt: expiresAt,
		}); err != nil {
			return err
		}

		targetType := "user"
		targetID := strconv.FormatInt(u.ID, 10)
		if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			Action:      "user.create",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata: map[string]any{
				"email":               u.Email,
				"is_admin":            u.IsAdmin,
				"can_create_projects": u.CanCreateProjects,
			},
		}); err != nil {
			return err
		}

		created = u
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "a user with this email already exists", nil)
			return
		}
		s.log.Error("admin create user failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	writeJSON(w, http.StatusCreated, adminCreateUserResponse{
		User:      toAdminUserResponse(store.AdminUser{User: created, ProjectCount: 0}),
		InviteURL: s.inviteURL(rawToken),
	})
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	targetID, err := parsePositiveInt64Param(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid user id", nil)
		return
	}

	var req adminUpdateUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	var nextNamePtr *string
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if n := utf8.RuneCountInString(trimmed); n < 1 || n > maxUserNameRunes {
			writeValidationError(w, map[string]string{
				"name": "must be between 1 and 80 characters",
			})
			return
		}
		nextNamePtr = &trimmed
	}

	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	var (
		updated         store.AdminUser
		notifySSEChange bool
	)
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		existing, err := tx.GetUserByID(ctx, targetID)
		if err != nil {
			return err
		}

		nextName := existing.Name
		if nextNamePtr != nil {
			nextName = *nextNamePtr
		}
		nextIsAdmin := existing.IsAdmin
		if req.IsAdmin != nil {
			nextIsAdmin = *req.IsAdmin
		}
		nextCanCreate := existing.CanCreateProjects
		if req.CanCreateProjects != nil {
			nextCanCreate = *req.CanCreateProjects
		}
		nextDisabledAt := existing.DisabledAt
		disabling := false
		enabling := false
		if req.Disabled != nil {
			if *req.Disabled && existing.DisabledAt == nil {
				t := now
				nextDisabledAt = &t
				disabling = true
			} else if !*req.Disabled && existing.DisabledAt != nil {
				nextDisabledAt = nil
				enabling = true
			}
		}

		// Prevent demoting or disabling the last active admin (FR-100).
		wasActiveAdmin := existing.IsAdmin && existing.DisabledAt == nil
		willBeActiveAdmin := nextIsAdmin && nextDisabledAt == nil
		if wasActiveAdmin && !willBeActiveAdmin {
			activeAdmins, err := tx.CountActiveAdmins(ctx)
			if err != nil {
				return err
			}
			if activeAdmins <= 1 {
				return errLastAdmin
			}
		}

		if _, err := tx.UpdateUserAdmin(ctx, store.UpdateUserAdminParams{
			ID:                targetID,
			Name:              nextName,
			IsAdmin:           nextIsAdmin,
			CanCreateProjects: nextCanCreate,
			DisabledAt:        nextDisabledAt,
			Now:               now,
		}); err != nil {
			return err
		}

		targetType := "user"
		targetIDStr := strconv.FormatInt(targetID, 10)
		if disabling {
			// FR-044: disabling a user deletes all their sessions and revokes their API tokens.
			if _, err := tx.DeleteAllSessionsForUser(ctx, targetID); err != nil {
				return err
			}
			if _, err := tx.RevokeAllAPITokensForUser(ctx, targetID, now); err != nil {
				return err
			}
			if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
				At:          now,
				ActorType:   actorType,
				ActorUserID: actorUserID,
				ActorLabel:  actorLabel,
				Action:      "user.disable",
				TargetType:  &targetType,
				TargetID:    &targetIDStr,
				IP:          ipPtr,
				Metadata:    map[string]any{"email": existing.Email},
			}); err != nil {
				return err
			}
		} else if enabling {
			if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
				At:          now,
				ActorType:   actorType,
				ActorUserID: actorUserID,
				ActorLabel:  actorLabel,
				Action:      "user.enable",
				TargetType:  &targetType,
				TargetID:    &targetIDStr,
				IP:          ipPtr,
				Metadata:    map[string]any{"email": existing.Email},
			}); err != nil {
				return err
			}
		}

		otherChanged := nextName != existing.Name || nextIsAdmin != existing.IsAdmin || nextCanCreate != existing.CanCreateProjects
		if otherChanged || (!disabling && !enabling) {
			if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
				At:          now,
				ActorType:   actorType,
				ActorUserID: actorUserID,
				ActorLabel:  actorLabel,
				Action:      "user.update",
				TargetType:  &targetType,
				TargetID:    &targetIDStr,
				IP:          ipPtr,
				Metadata: map[string]any{
					"is_admin":            nextIsAdmin,
					"can_create_projects": nextCanCreate,
				},
			}); err != nil {
				return err
			}
		}

		notifySSEChange = disabling || enabling || nextIsAdmin != existing.IsAdmin
		au, err := tx.GetUserWithProjectCount(ctx, targetID)
		if err != nil {
			return err
		}
		updated = au
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "user not found", nil)
			return
		}
		if errors.Is(err, errLastAdmin) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "cannot demote or disable the last active admin", nil)
			return
		}
		s.log.Error("admin update user failed", "request_id", RequestIDFromContext(ctx), "target_user_id", targetID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	if notifySSEChange {
		s.publishMembershipChanged(targetID, "")
	}
	writeJSON(w, http.StatusOK, toAdminUserResponse(updated))
}

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	targetID, err := parsePositiveInt64Param(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid user id", nil)
		return
	}

	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		existing, err := tx.GetUserByID(ctx, targetID)
		if err != nil {
			return err
		}

		// FR-100: deleting the last admin returns 409.
		if existing.IsAdmin {
			admins, err := tx.CountAdmins(ctx)
			if err != nil {
				return err
			}
			if admins <= 1 {
				return errLastAdmin
			}
			if existing.DisabledAt == nil {
				activeAdmins, err := tx.CountActiveAdmins(ctx)
				if err != nil {
					return err
				}
				if activeAdmins <= 1 {
					return errLastAdmin
				}
			}
		}

		if err := tx.DeleteUser(ctx, targetID); err != nil {
			return err
		}

		targetType := "user"
		targetIDStr := strconv.FormatInt(targetID, 10)
		_, err = tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			Action:      "user.delete",
			TargetType:  &targetType,
			TargetID:    &targetIDStr,
			IP:          ipPtr,
			Metadata:    map[string]any{"email": existing.Email},
		})
		return err
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "user not found", nil)
			return
		}
		if errors.Is(err, errLastAdmin) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "cannot delete the last admin", nil)
			return
		}
		s.log.Error("admin delete user failed", "request_id", RequestIDFromContext(ctx), "target_user_id", targetID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	s.publishMembershipChanged(targetID, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminPasswordReset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	targetID, err := parsePositiveInt64Param(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid user id", nil)
		return
	}

	user, err := s.store.GetUserByID(ctx, targetID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "user not found", nil)
			return
		}
		s.log.Error("admin password reset lookup failed", "request_id", RequestIDFromContext(ctx), "target_user_id", targetID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	rawToken, err := auth.GenerateToken()
	if err != nil {
		s.log.Error("admin password reset generate token failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	expiresAt := now.Add(auth.PasswordResetTTL)
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	if err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.CreatePasswordReset(ctx, store.CreatePasswordResetParams{
			UserID:    user.ID,
			TokenHash: auth.HashToken(rawToken),
			CreatedBy: actorUserID,
			Now:       now,
			ExpiresAt: expiresAt,
		}); err != nil {
			return err
		}
		targetType := "user"
		targetIDStr := strconv.FormatInt(user.ID, 10)
		_, err := tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			Action:      "user.password_reset_link",
			TargetType:  &targetType,
			TargetID:    &targetIDStr,
			IP:          ipPtr,
			Metadata:    map[string]any{"expires_at": expiresAt.Format(time.RFC3339)},
		})
		return err
	}); err != nil {
		s.log.Error("admin password reset create failed", "request_id", RequestIDFromContext(ctx), "target_user_id", targetID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	writeJSON(w, http.StatusOK, adminPasswordResetResponse{
		URL:       strings.TrimRight(s.cfg.BaseURL, "/") + "/reset/" + rawToken,
		ExpiresAt: Timestamp(expiresAt),
	})
}

func (s *Server) handleAdminListProjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		s.log.Error("admin list projects failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	ownersByProject, err := s.store.ListAllProjectOwners(ctx)
	if err != nil {
		s.log.Error("admin list project owners failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	out := make([]adminProjectResponse, 0, len(projects))
	for _, p := range projects {
		owners := make([]memberUserResponse, 0, len(ownersByProject[p.ID]))
		for _, o := range ownersByProject[p.ID] {
			owners = append(owners, memberUserResponse{
				ID:    o.UserID,
				Email: o.Email,
				Name:  o.Name,
			})
		}
		out = append(out, adminProjectResponse{
			ID:            p.ID,
			Slug:          p.Slug,
			Name:          p.Name,
			Color:         p.Color,
			MessageCount:  p.MessageCount,
			UnreadCount:   p.UnreadCount,
			SizeBytes:     p.SizeBytes,
			LastMessageAt: formatTimestampPtr(p.LastMessageAt),
			CreatedAt:     Timestamp(p.CreatedAt),
			Owners:        owners,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type adminSettingsValues struct {
	InstanceName         *string `json:"instance_name"`
	RetentionMaxAge      *int64  `json:"retention_max_age"`
	RetentionMaxMessages *int64  `json:"retention_max_messages"`
	RetentionMaxBytes    *int64  `json:"retention_max_bytes"`
}

type adminEffectiveSettings struct {
	InstanceName         string `json:"instance_name"`
	RetentionMaxAge      int64  `json:"retention_max_age"`
	RetentionMaxMessages int64  `json:"retention_max_messages"`
	RetentionMaxBytes    int64  `json:"retention_max_bytes"`
}

type adminSettingsSource struct {
	InstanceName         string `json:"instance_name"`
	RetentionMaxAge      string `json:"retention_max_age"`
	RetentionMaxMessages string `json:"retention_max_messages"`
	RetentionMaxBytes    string `json:"retention_max_bytes"`
}

type adminSettingsResponse struct {
	Settings  adminSettingsValues    `json:"settings"`
	Effective adminEffectiveSettings `json:"effective"`
	Source    adminSettingsSource    `json:"source"`
}

type adminUpdateSettingsRequest struct {
	InstanceName         Nullable[string] `json:"instance_name"`
	RetentionMaxAge      Nullable[int64]  `json:"retention_max_age"`
	RetentionMaxMessages Nullable[int64]  `json:"retention_max_messages"`
	RetentionMaxBytes    Nullable[int64]  `json:"retention_max_bytes"`
}

type adminStatsResponse struct {
	Projects         int64     `json:"projects"`
	Users            int64     `json:"users"`
	Messages         int64     `json:"messages"`
	StoredBytes      int64     `json:"stored_bytes"`
	DBSizeBytes      int64     `json:"db_size_bytes"`
	BlobSizeBytes    int64     `json:"blob_size_bytes"`
	IngestLastHour   int64     `json:"ingest_last_hour"`
	IngestRatePerMin float64   `json:"ingest_rate_per_minute"`
	InstanceMaxBytes int64     `json:"instance_max_bytes"`
	Version          string    `json:"version"`
	UptimeSeconds    int64     `json:"uptime_seconds"`
	StartedAt        Timestamp `json:"started_at"`
}

func (s *Server) handleAdminGetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	resolved, err := retention.ResolveInstanceSettings(ctx, s.store, s.cfg)
	if err != nil {
		s.log.Error("admin get settings failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	writeJSON(w, http.StatusOK, toAdminSettingsResponse(resolved))
}

func (s *Server) handleAdminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req adminUpdateSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	details := make(map[string]string)
	var trimmedInstanceName *string
	if req.InstanceName.Set && req.InstanceName.Value != nil {
		trimmed := strings.TrimSpace(*req.InstanceName.Value)
		if n := utf8.RuneCountInString(trimmed); n < 1 || n > maxUserNameRunes {
			details["instance_name"] = "must be between 1 and 80 characters"
		} else {
			trimmedInstanceName = &trimmed
		}
	}
	if req.RetentionMaxAge.Set && req.RetentionMaxAge.Value != nil && *req.RetentionMaxAge.Value < 0 {
		details["retention_max_age"] = "must be greater than or equal to 0"
	}
	if req.RetentionMaxMessages.Set && req.RetentionMaxMessages.Value != nil && *req.RetentionMaxMessages.Value < 0 {
		details["retention_max_messages"] = "must be greater than or equal to 0"
	}
	if req.RetentionMaxBytes.Set && req.RetentionMaxBytes.Value != nil && *req.RetentionMaxBytes.Value < 0 {
		details["retention_max_bytes"] = "must be greater than or equal to 0"
	}
	if len(details) > 0 {
		writeValidationError(w, details)
		return
	}

	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	var updatedKeys []string
	err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		applyString := func(key string, set bool, val *string) error {
			if !set {
				return nil
			}
			updatedKeys = append(updatedKeys, key)
			if val == nil {
				if err := tx.DeleteSetting(ctx, key); err != nil && !errors.Is(err, store.ErrNotFound) {
					return err
				}
				return nil
			}
			encoded, err := json.Marshal(*val)
			if err != nil {
				return err
			}
			return tx.SetSetting(ctx, key, string(encoded), now)
		}
		applyInt64 := func(key string, set bool, val *int64) error {
			if !set {
				return nil
			}
			updatedKeys = append(updatedKeys, key)
			if val == nil {
				if err := tx.DeleteSetting(ctx, key); err != nil && !errors.Is(err, store.ErrNotFound) {
					return err
				}
				return nil
			}
			encoded, err := json.Marshal(*val)
			if err != nil {
				return err
			}
			return tx.SetSetting(ctx, key, string(encoded), now)
		}

		if err := applyString(retention.SettingInstanceName, req.InstanceName.Set, trimmedInstanceName); err != nil {
			return err
		}
		if err := applyInt64(retention.SettingRetentionMaxAgeSec, req.RetentionMaxAge.Set, req.RetentionMaxAge.Value); err != nil {
			return err
		}
		if err := applyInt64(retention.SettingRetentionMaxMessages, req.RetentionMaxMessages.Set, req.RetentionMaxMessages.Value); err != nil {
			return err
		}
		if err := applyInt64(retention.SettingRetentionMaxBytes, req.RetentionMaxBytes.Set, req.RetentionMaxBytes.Value); err != nil {
			return err
		}

		targetType := "settings"
		targetID := "instance"
		_, err := tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			Action:      "settings.update",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata:    map[string]any{"updated_keys": updatedKeys},
		})
		return err
	})
	if err != nil {
		s.log.Error("admin update settings failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	resolved, err := retention.ResolveInstanceSettings(ctx, s.store, s.cfg)
	if err != nil {
		s.log.Error("admin reload settings failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	writeJSON(w, http.StatusOK, toAdminSettingsResponse(resolved))
}

func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	totals, err := s.store.GetInstanceProjectTotals(ctx)
	if err != nil {
		s.log.Error("admin stats project totals failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	userCount, err := s.store.CountUsers(ctx)
	if err != nil {
		s.log.Error("admin stats user count failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	dbSize, err := s.store.DBSizeBytes()
	if err != nil {
		s.log.Error("admin stats db size failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	var blobSize int64
	if s.blobs != nil {
		blobSize, err = s.blobs.TotalDiskBytes(ctx)
		if err != nil {
			s.log.Error("admin stats blob size failed", "request_id", RequestIDFromContext(ctx), "error", err)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
	}

	now := s.clk.Now().UTC()
	ingestLastHour, err := s.store.CountMessagesSince(ctx, now.Add(-time.Hour))
	if err != nil {
		s.log.Error("admin stats ingest last hour failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	uptimeSeconds := max(int64(0), int64(now.Sub(s.startedAt).Seconds()))
	ratePerMin := math.Round((float64(ingestLastHour)/60.0)*100) / 100

	writeJSON(w, http.StatusOK, adminStatsResponse{
		Projects:         totals.ProjectCount,
		Users:            userCount,
		Messages:         totals.TotalMessages,
		StoredBytes:      totals.TotalBytes,
		DBSizeBytes:      dbSize,
		BlobSizeBytes:    blobSize,
		IngestLastHour:   ingestLastHour,
		IngestRatePerMin: ratePerMin,
		InstanceMaxBytes: s.cfg.InstanceMaxBytes,
		Version:          s.version,
		UptimeSeconds:    uptimeSeconds,
		StartedAt:        Timestamp(s.startedAt),
	})
}

func toAdminSettingsResponse(r retention.ResolvedInstanceSettings) adminSettingsResponse {
	return adminSettingsResponse{
		Settings: adminSettingsValues{
			InstanceName:         r.Overrides.InstanceName,
			RetentionMaxAge:      r.Overrides.RetentionMaxAgeSec,
			RetentionMaxMessages: r.Overrides.RetentionMaxMessages,
			RetentionMaxBytes:    r.Overrides.RetentionMaxBytes,
		},
		Effective: adminEffectiveSettings{
			InstanceName:         r.Effective.InstanceName,
			RetentionMaxAge:      r.Effective.RetentionMaxAgeSec,
			RetentionMaxMessages: r.Effective.RetentionMaxMessages,
			RetentionMaxBytes:    r.Effective.RetentionMaxBytes,
		},
		Source: adminSettingsSource{
			InstanceName:         r.Source.InstanceName,
			RetentionMaxAge:      r.Source.RetentionMaxAge,
			RetentionMaxMessages: r.Source.RetentionMaxMessages,
			RetentionMaxBytes:    r.Source.RetentionMaxBytes,
		},
	}
}

func toAdminUserResponse(u store.AdminUser) adminUserResponse {
	return adminUserResponse{
		ID:                u.User.ID,
		Email:             u.User.Email,
		Name:              u.User.Name,
		IsAdmin:           u.User.IsAdmin,
		CanCreateProjects: u.User.CanCreateProjects,
		HasPassword:       u.User.PasswordHash != nil && *u.User.PasswordHash != "",
		DisabledAt:        formatTimestampPtr(u.User.DisabledAt),
		LastLoginAt:       formatTimestampPtr(u.User.LastLoginAt),
		CreatedAt:         Timestamp(u.User.CreatedAt),
		ProjectCount:      u.ProjectCount,
	}
}

func encodeIntCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

func decodeIntCursor(raw string) (int64, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid integer cursor")
	}
	return id, nil
}
