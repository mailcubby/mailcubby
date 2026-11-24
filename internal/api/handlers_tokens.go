package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/store"
)

const (
	maxAPITokenNameRunes  = 80
	maxAPITokenExpiryDays = 3650
	maxAPITokenGenRetries = 5
)

type apiTokenInfoResponse struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scope      string     `json:"scope"`
	Project    *string    `json:"project"`
	CreatedAt  Timestamp  `json:"created_at"`
	LastUsedAt *Timestamp `json:"last_used_at"`
	ExpiresAt  *Timestamp `json:"expires_at"`
}

type createAPITokenRequest struct {
	Name          string  `json:"name"`
	Scope         string  `json:"scope"`
	Project       *string `json:"project"`
	ExpiresInDays *int    `json:"expires_in_days"`
}

type createAPITokenResponse struct {
	TokenInfo apiTokenInfoResponse `json:"token_info"`
	Token     string               `json:"token"`
}

func (s *Server) handleListAPITokens(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	principal := PrincipalFromContext(ctx)

	tokens, err := s.store.ListAPITokensByUser(ctx, principal.UserID)
	if err != nil {
		s.log.Error("list api tokens failed",
			"request_id", RequestIDFromContext(ctx),
			"user_id", principal.UserID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	out := make([]apiTokenInfoResponse, 0, len(tokens))
	for _, tok := range tokens {
		out = append(out, toAPITokenInfoResponse(tok))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateAPIToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	principal := PrincipalFromContext(ctx)

	var req createAPITokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	details := make(map[string]string)
	name := strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxAPITokenNameRunes {
		details["name"] = "must be between 1 and 80 characters"
	}

	scope := strings.TrimSpace(req.Scope)
	if !auth.ValidAPITokenScope(scope) {
		details["scope"] = "must be one of: read, write"
	}

	now := s.clk.Now().UTC()
	var expiresAt *time.Time
	if req.ExpiresInDays != nil {
		days := *req.ExpiresInDays
		if days < 1 || days > maxAPITokenExpiryDays {
			details["expires_in_days"] = "must be between 1 and 3650 days"
		} else {
			t := now.Add(time.Duration(days) * 24 * time.Hour)
			expiresAt = &t
		}
	}

	if len(details) > 0 {
		writeValidationError(w, details)
		return
	}

	var (
		projectID   *int64
		projectSlug *string
	)
	if req.Project != nil && strings.TrimSpace(*req.Project) != "" {
		slug := strings.TrimSpace(*req.Project)
		proj, err := s.store.GetProjectBySlug(ctx, slug)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
				return
			}
			s.log.Error("lookup project for api token failed",
				"request_id", RequestIDFromContext(ctx),
				"slug", slug,
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
		if !principal.IsAdmin {
			if _, memErr := s.store.GetProjectMember(ctx, proj.ID, principal.UserID); memErr != nil {
				if errors.Is(memErr, store.ErrNotFound) {
					writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
					return
				}
				s.log.Error("check membership for api token failed",
					"request_id", RequestIDFromContext(ctx),
					"project_id", proj.ID,
					"user_id", principal.UserID,
					"error", memErr,
				)
				writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
				return
			}
		}
		pid := proj.ID
		pslug := proj.Slug
		projectID = &pid
		projectSlug = &pslug
	}

	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	var (
		created store.APIToken
		rawTok  string
		txErr   error
	)
	for range maxAPITokenGenRetries {
		raw, prefix, hash, genErr := auth.GenerateAPIToken()
		if genErr != nil {
			s.log.Error("generate api token failed",
				"request_id", RequestIDFromContext(ctx),
				"user_id", principal.UserID,
				"error", genErr,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}

		txErr = s.store.WithTx(ctx, func(tx *store.Tx) error {
			tok, err := tx.CreateAPIToken(ctx, store.CreateAPITokenParams{
				UserID:    principal.UserID,
				ProjectID: projectID,
				Name:      name,
				Prefix:    prefix,
				TokenHash: hash,
				Scope:     scope,
				Now:       now,
				ExpiresAt: expiresAt,
			})
			if err != nil {
				return err
			}

			targetType := "api_token"
			targetID := strconv.FormatInt(tok.ID, 10)
			meta := map[string]any{
				"name":   tok.Name,
				"prefix": tok.Prefix,
				"scope":  tok.Scope,
			}
			if projectSlug != nil {
				meta["project"] = *projectSlug
			}
			if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
				At:          now,
				ActorType:   actorType,
				ActorUserID: actorUserID,
				ActorLabel:  actorLabel,
				ProjectID:   projectID,
				Action:      "api_token.create",
				TargetType:  &targetType,
				TargetID:    &targetID,
				IP:          ipPtr,
				Metadata:    meta,
			}); err != nil {
				return err
			}

			tok.ProjectSlug = projectSlug
			created = tok
			rawTok = raw
			return nil
		})
		if txErr == nil || !errors.Is(txErr, store.ErrConflict) {
			break
		}
	}

	if txErr != nil {
		s.log.Error("create api token failed",
			"request_id", RequestIDFromContext(ctx),
			"user_id", principal.UserID,
			"error", txErr,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusCreated, createAPITokenResponse{
		TokenInfo: toAPITokenInfoResponse(created),
		Token:     rawTok,
	})
}

func (s *Server) handleDeleteAPIToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	principal := PrincipalFromContext(ctx)

	tokenID, err := parsePositiveInt64Param(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "API token not found", nil)
		return
	}

	now := s.clk.Now().UTC()
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		tok, err := tx.GetAPITokenByIDForUser(ctx, tokenID, principal.UserID)
		if err != nil {
			return err
		}
		if err := tx.RevokeAPITokenForUser(ctx, tokenID, principal.UserID, now); err != nil {
			return err
		}
		targetType := "api_token"
		targetID := strconv.FormatInt(tokenID, 10)
		meta := map[string]any{
			"name":   tok.Name,
			"prefix": tok.Prefix,
			"scope":  tok.Scope,
		}
		if tok.ProjectSlug != nil {
			meta["project"] = *tok.ProjectSlug
		}
		_, err = tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			ProjectID:   tok.ProjectID,
			Action:      "api_token.revoke",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata:    meta,
		})
		return err
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "API token not found", nil)
			return
		}
		s.log.Error("revoke api token failed",
			"request_id", RequestIDFromContext(ctx),
			"user_id", principal.UserID,
			"token_id", tokenID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func toAPITokenInfoResponse(tok store.APIToken) apiTokenInfoResponse {
	return apiTokenInfoResponse{
		ID:         tok.ID,
		Name:       tok.Name,
		Prefix:     tok.Prefix,
		Scope:      tok.Scope,
		Project:    tok.ProjectSlug,
		CreatedAt:  Timestamp(tok.CreatedAt),
		LastUsedAt: formatTimestampPtr(tok.LastUsedAt),
		ExpiresAt:  formatTimestampPtr(tok.ExpiresAt),
	}
}
