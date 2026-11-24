package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/store"
)

type memberUserResponse struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type projectMemberResponse struct {
	User      memberUserResponse `json:"user"`
	Role      string             `json:"role"`
	CreatedAt Timestamp          `json:"created_at"`
}

type patchProjectMemberRequest struct {
	Role string `json:"role"`
}

func (s *Server) handleListProjectMembers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	members, err := s.store.ListProjectMembers(ctx, pctx.Project.ID)
	if err != nil {
		s.log.Error("list project members failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	out := make([]projectMemberResponse, 0, len(members))
	for _, m := range members {
		out = append(out, toProjectMemberResponse(m))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateProjectMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	targetUserID, err := parsePositiveInt64Param(r.PathValue("user_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid user_id", nil)
		return
	}

	var req patchProjectMemberRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	nextRole := auth.Role(strings.TrimSpace(req.Role))
	if !auth.ValidMemberRole(nextRole) {
		writeValidationError(w, map[string]string{
			"role": "must be one of owner, member, viewer",
		})
		return
	}

	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	var updated store.ProjectMember
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		existing, err := tx.GetProjectMember(ctx, pctx.Project.ID, targetUserID)
		if err != nil {
			return err
		}

		// SPEC 6.5: removing or demoting the last owner returns 409.
		if existing.Role == string(auth.RoleOwner) && nextRole != auth.RoleOwner {
			owners, err := tx.CountProjectOwners(ctx, pctx.Project.ID)
			if err != nil {
				return err
			}
			if owners <= 1 {
				return errLastProjectOwner
			}
		}

		if existing.Role != string(nextRole) {
			if err := tx.UpdateProjectMemberRole(ctx, pctx.Project.ID, targetUserID, string(nextRole)); err != nil {
				return err
			}
			targetType := "user"
			targetID := strconv.FormatInt(targetUserID, 10)
			if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
				At:          now,
				ActorType:   actorType,
				ActorUserID: actorUserID,
				ActorLabel:  actorLabel,
				ProjectID:   &pctx.Project.ID,
				Action:      "member.update",
				TargetType:  &targetType,
				TargetID:    &targetID,
				IP:          ipPtr,
				Metadata: map[string]any{
					"old_role": existing.Role,
					"new_role": string(nextRole),
				},
			}); err != nil {
				return err
			}
			existing.Role = string(nextRole)
		}

		updated = existing
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "member not found", nil)
			return
		}
		if errors.Is(err, errLastProjectOwner) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "project must have at least one owner", nil)
			return
		}
		s.log.Error("update project member failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"target_user_id", targetUserID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	s.publishMembershipChanged(targetUserID, pctx.Project.Slug)
	writeJSON(w, http.StatusOK, toProjectMemberResponse(updated))
}

func (s *Server) handleDeleteProjectMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	targetUserID, err := parsePositiveInt64Param(r.PathValue("user_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid user_id", nil)
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
		existing, err := tx.GetProjectMember(ctx, pctx.Project.ID, targetUserID)
		if err != nil {
			return err
		}

		// SPEC 6.5: removing the last owner returns 409.
		if existing.Role == string(auth.RoleOwner) {
			owners, err := tx.CountProjectOwners(ctx, pctx.Project.ID)
			if err != nil {
				return err
			}
			if owners <= 1 {
				return errLastProjectOwner
			}
		}

		if err := tx.DeleteProjectMember(ctx, pctx.Project.ID, targetUserID); err != nil {
			return err
		}

		targetType := "user"
		targetID := strconv.FormatInt(targetUserID, 10)
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
			Metadata:    map[string]any{"role": existing.Role},
		})
		return err
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "member not found", nil)
			return
		}
		if errors.Is(err, errLastProjectOwner) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "cannot remove the last owner", nil)
			return
		}
		s.log.Error("delete project member failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"target_user_id", targetUserID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	s.publishMembershipChanged(targetUserID, pctx.Project.Slug)
	w.WriteHeader(http.StatusNoContent)
}

func toProjectMemberResponse(m store.ProjectMember) projectMemberResponse {
	return projectMemberResponse{
		User: memberUserResponse{
			ID:    m.UserID,
			Email: m.UserEmail,
			Name:  m.UserName,
		},
		Role:      m.Role,
		CreatedAt: Timestamp(m.CreatedAt),
	}
}

func parsePositiveInt64Param(raw string) (int64, error) {
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || v <= 0 {
		return 0, errors.New("invalid integer id")
	}
	return v, nil
}
