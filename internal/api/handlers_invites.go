package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/ratelimit"
	"github.com/mailcubby/mailcubby/internal/store"
)

var (
	errInviteGone            = errors.New("invite link is invalid or has expired")
	errInviteAlreadyAccepted = errors.New("invite has already been accepted")
	errInviteLoginRequired   = errors.New("existing account requires login before accepting invite")
	errInviteAccountDisabled = errors.New("user account is disabled")
)

type inviteResponse struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedBy *string   `json:"created_by"`
	CreatedAt Timestamp `json:"created_at"`
	ExpiresAt Timestamp `json:"expires_at"`
	Status    string    `json:"status"`
}

type createProjectInviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type createProjectInviteResponse struct {
	Invite inviteResponse `json:"invite"`
	URL    string         `json:"url"`
}

type inviteProjectPreview struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type invitePreviewResponse struct {
	Project   *inviteProjectPreview `json:"project,omitempty"`
	Role      *string               `json:"role"`
	Email     string                `json:"email"`
	InvitedBy *string               `json:"invited_by"`
	ExpiresAt Timestamp             `json:"expires_at"`
}

type acceptInviteRequest struct {
	Name     *string `json:"name"`
	Password *string `json:"password"`
}

type acceptInviteResponse struct {
	Project *inviteProjectPreview `json:"project,omitempty"`
}

func (s *Server) handleListProjectInvites(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	invites, err := s.store.ListProjectInvites(ctx, pctx.Project.ID)
	if err != nil {
		s.log.Error("list project invites failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	now := s.clk.Now().UTC()
	out := make([]inviteResponse, 0, len(invites))
	for _, inv := range invites {
		out = append(out, toProjectInviteResponse(inv, now))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateProjectInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	var req createProjectInviteRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	details := make(map[string]string)
	email, err := auth.ValidateEmail(req.Email)
	if err != nil {
		details["email"] = err.Error()
	}
	role := auth.Role(strings.TrimSpace(req.Role))
	if !auth.ValidMemberRole(role) {
		details["role"] = "must be one of owner, member, viewer"
	}
	if len(details) > 0 {
		writeValidationError(w, details)
		return
	}

	rawToken, err := auth.GenerateToken()
	if err != nil {
		s.log.Error("generate invite token failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	expiresAt := now.Add(s.cfg.InviteTTL)
	roleStr := string(role)
	projectID := pctx.Project.ID
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	var created store.Invite
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		// FR-055: creating a project invite for an email with a pending invite
		// to the same project revokes the older one.
		if _, err := tx.RevokePendingProjectInvitesForEmail(ctx, projectID, email, now); err != nil {
			return err
		}

		inv, err := tx.CreateInvite(ctx, store.CreateInviteParams{
			ProjectID: &projectID,
			Email:     email,
			Role:      &roleStr,
			TokenHash: auth.HashToken(rawToken),
			CreatedBy: actorUserID,
			Now:       now,
			ExpiresAt: expiresAt,
		})
		if err != nil {
			return err
		}

		targetType := "invite"
		targetID := strconv.FormatInt(inv.ID, 10)
		if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			ProjectID:   &projectID,
			Action:      "invite.create",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata:    map[string]any{"email": email, "role": roleStr},
		}); err != nil {
			return err
		}

		created = inv
		return nil
	})
	if err != nil {
		s.log.Error("create project invite failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", projectID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	if principal.Name != "" {
		n := principal.Name
		created.CreatedByName = &n
	}
	if principal.Email != "" {
		e := principal.Email
		created.CreatedByEmail = &e
	}

	writeJSON(w, http.StatusCreated, createProjectInviteResponse{
		Invite: toProjectInviteResponse(created, now),
		URL:    s.inviteURL(rawToken),
	})
}

func (s *Server) handleRevokeProjectInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	inviteID, err := parsePositiveInt64Param(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid invite id", nil)
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
		inv, err := tx.GetProjectInviteByID(ctx, pctx.Project.ID, inviteID)
		if err != nil {
			return err
		}
		if inv.AcceptedAt != nil {
			return errInviteAlreadyAccepted
		}
		if inv.RevokedAt != nil {
			return nil
		}
		revoked, err := tx.RevokeProjectInvite(ctx, pctx.Project.ID, inviteID, now)
		if err != nil {
			return err
		}
		if !revoked {
			return errInviteAlreadyAccepted
		}
		targetType := "invite"
		targetID := strconv.FormatInt(inviteID, 10)
		_, err = tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			ProjectID:   &pctx.Project.ID,
			Action:      "invite.revoke",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata:    map[string]any{"email": inv.Email},
		})
		return err
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "invite not found", nil)
			return
		}
		if errors.Is(err, errInviteAlreadyAccepted) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "invite has already been accepted", nil)
			return
		}
		s.log.Error("revoke project invite failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"invite_id", inviteID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePreviewInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.allowInviteRateLimit(w, r) {
		return
	}

	rawToken := strings.TrimSpace(r.PathValue("token"))
	if rawToken == "" {
		writeError(w, http.StatusGone, ErrCodeGone, "invite link is invalid or has expired", nil)
		return
	}

	inv, err := s.store.GetInviteByTokenHash(ctx, auth.HashToken(rawToken))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusGone, ErrCodeGone, "invite link is invalid or has expired", nil)
			return
		}
		s.log.Error("preview invite lookup failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	now := s.clk.Now().UTC()
	if inv.Status(now) != "pending" {
		writeError(w, http.StatusGone, ErrCodeGone, "invite link is invalid or has expired", nil)
		return
	}

	var proj *inviteProjectPreview
	if inv.ProjectID != nil && inv.ProjectSlug != nil && inv.ProjectName != nil {
		proj = &inviteProjectPreview{
			Name: *inv.ProjectName,
			Slug: *inv.ProjectSlug,
		}
	}

	writeJSON(w, http.StatusOK, invitePreviewResponse{
		Project:   proj,
		Role:      inv.Role,
		Email:     inv.Email,
		InvitedBy: inv.InviterLabel(),
		ExpiresAt: Timestamp(inv.ExpiresAt),
	})
}

func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.allowInviteRateLimit(w, r) {
		return
	}

	principal := PrincipalFromContext(ctx)
	if principal.Kind == auth.PrincipalToken {
		writeError(w, http.StatusForbidden, ErrCodeForbidden, "API tokens cannot accept invites", nil)
		return
	}

	var req acceptInviteRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}

	rawToken := strings.TrimSpace(r.PathValue("token"))
	if rawToken == "" {
		writeError(w, http.StatusGone, ErrCodeGone, "invite link is invalid or has expired", nil)
		return
	}

	inv, err := s.store.GetInviteByTokenHash(ctx, auth.HashToken(rawToken))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusGone, ErrCodeGone, "invite link is invalid or has expired", nil)
			return
		}
		s.log.Error("accept invite lookup failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	now := s.clk.Now().UTC()
	if inv.Status(now) != "pending" {
		writeError(w, http.StatusGone, ErrCodeGone, "invite link is invalid or has expired", nil)
		return
	}

	clientIP := ClientIPFromContext(ctx)
	var ipPtr *string
	if clientIP != "" {
		ipPtr = &clientIP
	}

	// Case 1: caller is already logged in via browser session (FR-056).
	if principal.Kind == auth.PrincipalUser {
		if inv.ProjectID == nil || inv.Role == nil {
			writeError(w, http.StatusConflict, ErrCodeConflict, "instance invite cannot be accepted while signed in", nil)
			return
		}
		if err := s.acceptInviteLoggedIn(ctx, inv, principal, ipPtr, now); err != nil {
			if errors.Is(err, errInviteGone) {
				writeError(w, http.StatusGone, ErrCodeGone, "invite link is invalid or has expired", nil)
				return
			}
			s.log.Error("accept invite (logged in) failed",
				"request_id", RequestIDFromContext(ctx),
				"invite_id", inv.ID,
				"user_id", principal.UserID,
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
		s.publishMembershipChanged(principal.UserID, derefString(inv.ProjectSlug))
		writeJSON(w, http.StatusOK, acceptInviteResponse{
			Project: invitePreviewProject(inv),
		})
		return
	}

	// Case 2: caller is not logged in (FR-056).
	existingUser, lookupErr := s.store.GetUserByEmail(ctx, inv.Email)
	userExists := lookupErr == nil
	if lookupErr != nil && !errors.Is(lookupErr, store.ErrNotFound) {
		s.log.Error("accept invite user lookup failed", "request_id", RequestIDFromContext(ctx), "error", lookupErr)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	if userExists && existingUser.PasswordHash != nil && *existingUser.PasswordHash != "" {
		// FR-056: existing account with password must sign in first.
		writeError(w, http.StatusConflict, ErrCodeLoginRequired, "an account with this email already exists; sign in to accept the invite", nil)
		return
	}
	if userExists && existingUser.DisabledAt != nil {
		writeError(w, http.StatusForbidden, ErrCodeForbidden, "account is disabled", nil)
		return
	}

	details := make(map[string]string)
	var trimmedName *string
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		n := strings.TrimSpace(*req.Name)
		if utf8.RuneCountInString(n) > maxUserNameRunes {
			details["name"] = "must be between 1 and 80 characters"
		} else {
			trimmedName = &n
		}
	} else if !userExists {
		details["name"] = "must be between 1 and 80 characters"
	}

	var password string
	if req.Password == nil || *req.Password == "" {
		details["password"] = "must be between 10 and 256 characters"
	} else if err := auth.ValidatePassword(*req.Password); err != nil {
		details["password"] = "must be between 10 and 256 characters"
	} else {
		password = *req.Password
	}

	if len(details) > 0 {
		writeValidationError(w, details)
		return
	}

	// Hash outside the DB transaction so we never hold the SQLite writer lock during argon2id.
	passwordHash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		s.log.Error("accept invite password hash failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	userAgent := truncateString(strings.TrimSpace(r.UserAgent()), maxUserAgentLen)
	var (
		acceptedUserID int64
		rawSessionTok  string
	)
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		// Atomic single-use guard runs first inside the serialized write transaction.
		consumed, err := tx.ConsumeInvite(ctx, inv.ID, nil, now)
		if err != nil {
			return err
		}
		if !consumed {
			return errInviteGone
		}

		var user store.User
		u, uErr := tx.GetUserByEmail(ctx, inv.Email)
		switch {
		case uErr == nil:
			if u.PasswordHash != nil && *u.PasswordHash != "" {
				return errInviteLoginRequired
			}
			if u.DisabledAt != nil {
				return errInviteAccountDisabled
			}
			if trimmedName != nil {
				u, err = tx.UpdateUserName(ctx, u.ID, *trimmedName, now)
				if err != nil {
					return err
				}
			}
			if err := tx.UpdateUserPassword(ctx, u.ID, passwordHash, now); err != nil {
				return err
			}
			if err := tx.UpdateUserLastLogin(ctx, u.ID, now); err != nil {
				return err
			}
			user = u
		case errors.Is(uErr, store.ErrNotFound):
			if trimmedName == nil {
				return errInviteLoginRequired
			}
			created, err := tx.CreateUser(ctx, store.CreateUserParams{
				Email:             inv.Email,
				Name:              *trimmedName,
				PasswordHash:      &passwordHash,
				IsAdmin:           false,
				CanCreateProjects: false,
				LastLoginAt:       &now,
				Now:               now,
			})
			if err != nil {
				if errors.Is(err, store.ErrConflict) {
					return errInviteLoginRequired
				}
				return err
			}
			user = created
		default:
			return uErr
		}

		if err := tx.SetInviteAcceptedBy(ctx, inv.ID, user.ID); err != nil {
			return err
		}

		if inv.ProjectID != nil && inv.Role != nil {
			if err := upsertMembershipUpgradeTx(ctx, tx, *inv.ProjectID, user.ID, *inv.Role, store.AuditActorUser, &user.ID, &user.Email, ipPtr, now); err != nil {
				return err
			}
		}

		tok, err := s.createSessionTx(ctx, tx, user.ID, clientIP, userAgent, now)
		if err != nil {
			return err
		}

		targetType := "invite"
		targetID := strconv.FormatInt(inv.ID, 10)
		meta := map[string]any{"email": inv.Email}
		if inv.Role != nil {
			meta["role"] = *inv.Role
		} else {
			meta["kind"] = "instance"
		}
		if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   store.AuditActorUser,
			ActorUserID: &user.ID,
			ActorLabel:  &user.Email,
			ProjectID:   inv.ProjectID,
			Action:      "invite.accept",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata:    meta,
		}); err != nil {
			return err
		}

		acceptedUserID = user.ID
		rawSessionTok = tok
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errInviteGone):
			writeError(w, http.StatusGone, ErrCodeGone, "invite link is invalid or has expired", nil)
		case errors.Is(err, errInviteLoginRequired):
			writeError(w, http.StatusConflict, ErrCodeLoginRequired, "an account with this email already exists; sign in to accept the invite", nil)
		case errors.Is(err, errInviteAccountDisabled):
			writeError(w, http.StatusForbidden, ErrCodeForbidden, "account is disabled", nil)
		default:
			s.log.Error("accept invite commit failed",
				"request_id", RequestIDFromContext(ctx),
				"invite_id", inv.ID,
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		}
		return
	}

	if inv.ProjectID != nil {
		s.publishMembershipChanged(acceptedUserID, derefString(inv.ProjectSlug))
	}
	http.SetCookie(w, auth.NewSessionCookie(rawSessionTok, s.cfg.BaseURL, s.cfg.SessionTTL))
	writeJSON(w, http.StatusOK, acceptInviteResponse{
		Project: invitePreviewProject(inv),
	})
}

func (s *Server) acceptInviteLoggedIn(ctx context.Context, inv store.Invite, principal Principal, ipPtr *string, now time.Time) error {
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	return s.store.WithTx(ctx, func(tx *store.Tx) error {
		uid := principal.UserID
		consumed, err := tx.ConsumeInvite(ctx, inv.ID, &uid, now)
		if err != nil {
			return err
		}
		if !consumed {
			return errInviteGone
		}

		if err := upsertMembershipUpgradeTx(ctx, tx, *inv.ProjectID, principal.UserID, *inv.Role, actorType, actorUserID, actorLabel, ipPtr, now); err != nil {
			return err
		}

		targetType := "invite"
		targetID := strconv.FormatInt(inv.ID, 10)
		_, err = tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			ProjectID:   inv.ProjectID,
			Action:      "invite.accept",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata:    map[string]any{"email": inv.Email, "role": *inv.Role},
		})
		return err
	})
}

// upsertMembershipUpgradeTx adds userID to projectID with role, or upgrades their
// existing role if the invited role is strictly higher (never downgrades, SPEC FR-056).
func upsertMembershipUpgradeTx(
	ctx context.Context,
	tx *store.Tx,
	projectID, userID int64,
	invitedRole string,
	actorType store.AuditActorType,
	actorUserID *int64,
	actorLabel *string,
	ipPtr *string,
	now time.Time,
) error {
	existing, err := tx.GetProjectMember(ctx, projectID, userID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		if _, err := tx.AddProjectMember(ctx, projectID, userID, invitedRole, now); err != nil {
			return err
		}
		targetType := "user"
		targetID := strconv.FormatInt(userID, 10)
		_, err = tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			ProjectID:   &projectID,
			Action:      "member.add",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata:    map[string]any{"role": invitedRole, "via": "invite"},
		})
		return err
	case err != nil:
		return err
	default:
		if auth.RoleRank(auth.Role(invitedRole)) > auth.RoleRank(auth.Role(existing.Role)) {
			if err := tx.UpdateProjectMemberRole(ctx, projectID, userID, invitedRole); err != nil {
				return err
			}
			targetType := "user"
			targetID := strconv.FormatInt(userID, 10)
			_, err = tx.InsertAudit(ctx, store.InsertAuditParams{
				At:          now,
				ActorType:   actorType,
				ActorUserID: actorUserID,
				ActorLabel:  actorLabel,
				ProjectID:   &projectID,
				Action:      "member.update",
				TargetType:  &targetType,
				TargetID:    &targetID,
				IP:          ipPtr,
				Metadata: map[string]any{
					"old_role": existing.Role,
					"new_role": invitedRole,
					"via":      "invite",
				},
			})
			return err
		}
		return nil
	}
}

func (s *Server) allowInviteRateLimit(w http.ResponseWriter, r *http.Request) bool {
	clientIP := ClientIPFromContext(r.Context())
	if ok, retryAfter := s.inviteIPLimiter.Allow(clientIP); !ok {
		sec := ratelimit.RetryAfterSeconds(retryAfter)
		w.Header().Set("Retry-After", strconv.Itoa(sec))
		writeError(w, http.StatusTooManyRequests, ErrCodeRateLimited, "too many invite requests, try again later", map[string]string{
			"retry_after": strconv.Itoa(sec),
		})
		return false
	}
	return true
}

func (s *Server) inviteURL(rawToken string) string {
	return strings.TrimRight(s.cfg.BaseURL, "/") + "/invite/" + rawToken
}

func toProjectInviteResponse(inv store.Invite, now time.Time) inviteResponse {
	return inviteResponse{
		ID:        inv.ID,
		Email:     inv.Email,
		Role:      derefString(inv.Role),
		CreatedBy: inv.InviterLabel(),
		CreatedAt: Timestamp(inv.CreatedAt),
		ExpiresAt: Timestamp(inv.ExpiresAt),
		Status:    inv.Status(now),
	}
}

func invitePreviewProject(inv store.Invite) *inviteProjectPreview {
	if inv.ProjectID == nil || inv.ProjectSlug == nil || inv.ProjectName == nil {
		return nil
	}
	return &inviteProjectPreview{
		Name: *inv.ProjectName,
		Slug: *inv.ProjectSlug,
	}
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
