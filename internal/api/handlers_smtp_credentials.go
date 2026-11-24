package api

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mailcubby/mailcubby/internal/store"
)

const (
	maxSMTPCredentialsPerProject = 20
	maxSMTPCredentialLabelRunes  = 80
	smtpUsernameSuffixLen        = 8
	smtpPasswordLen              = 24
	maxUsernameGenAttempts       = 5

	smtpUsernameAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	smtpPasswordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

var errSMTPCredentialLimit = errors.New("maximum number of SMTP credentials per project reached")

type smtpCredentialResponse struct {
	ID         int64      `json:"id"`
	Label      string     `json:"label"`
	Username   string     `json:"username"`
	CreatedAt  Timestamp  `json:"created_at"`
	LastUsedAt *Timestamp `json:"last_used_at"`
	Disabled   bool       `json:"disabled"`
}

type smtpCredentialListResponse struct {
	Connection smtpConnectionInfo       `json:"connection"`
	Items      []smtpCredentialResponse `json:"items"`
}

type createSMTPCredentialRequest struct {
	Label string `json:"label"`
}

type createSMTPCredentialResponse struct {
	Credential smtpCredentialResponse `json:"credential"`
	Password   string                 `json:"password"`
}

type revealSMTPCredentialResponse struct {
	Password string `json:"password"`
}

type patchSMTPCredentialRequest struct {
	Label    *string `json:"label"`
	Disabled *bool   `json:"disabled"`
}

func (s *Server) handleListSMTPCredentials(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	creds, err := s.store.ListSMTPCredentialsByProject(ctx, pctx.Project.ID)
	if err != nil {
		s.log.Error("list smtp credentials failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	items := make([]smtpCredentialResponse, 0, len(creds))
	for _, c := range creds {
		items = append(items, toSMTPCredentialResponse(c))
	}

	writeJSON(w, http.StatusOK, smtpCredentialListResponse{
		Connection: s.smtpConnectionInfo(),
		Items:      items,
	})
}

func (s *Server) handleCreateSMTPCredential(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}
	if s.smtpCipher == nil {
		s.log.Error("smtp credential cipher not initialized",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	var req createSMTPCredentialRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	label := strings.TrimSpace(req.Label)
	if errMsg := validateSMTPCredentialLabel(label); errMsg != "" {
		writeValidationError(w, map[string]string{"label": errMsg})
		return
	}

	password, err := randomString(smtpPasswordAlphabet, smtpPasswordLen)
	if err != nil {
		s.log.Error("generate smtp credential password failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	var (
		created store.SMTPCredential
		txErr   error
	)
	for range maxUsernameGenAttempts {
		suffix, genErr := randomString(smtpUsernameAlphabet, smtpUsernameSuffixLen)
		if genErr != nil {
			s.log.Error("generate smtp credential username failed",
				"request_id", RequestIDFromContext(ctx),
				"project_id", pctx.Project.ID,
				"error", genErr,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
		username := pctx.Project.Slug + "-" + suffix
		sealed, sealErr := s.smtpCipher.Seal([]byte(password), []byte(username))
		if sealErr != nil {
			s.log.Error("seal smtp credential password failed",
				"request_id", RequestIDFromContext(ctx),
				"project_id", pctx.Project.ID,
				"error", sealErr,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}

		txErr = s.store.WithTx(ctx, func(tx *store.Tx) error {
			count, err := tx.CountSMTPCredentialsByProject(ctx, pctx.Project.ID)
			if err != nil {
				return err
			}
			if count >= maxSMTPCredentialsPerProject {
				return errSMTPCredentialLimit
			}

			c, err := tx.CreateSMTPCredential(ctx, store.CreateSMTPCredentialParams{
				ProjectID:   pctx.Project.ID,
				Label:       label,
				Username:    username,
				PasswordEnc: sealed,
				CreatedBy:   actorUserID,
				Now:         now,
			})
			if err != nil {
				return err
			}

			targetType := "smtp_credential"
			targetID := strconv.FormatInt(c.ID, 10)
			if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
				At:          now,
				ActorType:   actorType,
				ActorUserID: actorUserID,
				ActorLabel:  actorLabel,
				ProjectID:   &pctx.Project.ID,
				Action:      "smtp_credential.create",
				TargetType:  &targetType,
				TargetID:    &targetID,
				IP:          ipPtr,
				Metadata: map[string]any{
					"label":    c.Label,
					"username": c.Username,
				},
			}); err != nil {
				return err
			}

			created = c
			return nil
		})
		if txErr == nil || !errors.Is(txErr, store.ErrConflict) {
			break
		}
	}

	if txErr != nil {
		if errors.Is(txErr, errSMTPCredentialLimit) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "a project can have at most 20 SMTP credentials", nil)
			return
		}
		s.log.Error("create smtp credential failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"error", txErr,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusCreated, createSMTPCredentialResponse{
		Credential: toSMTPCredentialResponse(created),
		Password:   password,
	})
}

func (s *Server) handleRevealSMTPCredential(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}
	if s.smtpCipher == nil {
		s.log.Error("smtp credential cipher not initialized",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	credID, err := parsePositiveInt64Param(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid credential id", nil)
		return
	}

	cred, err := s.store.GetSMTPCredentialByID(ctx, pctx.Project.ID, credID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "credential not found", nil)
			return
		}
		s.log.Error("get smtp credential for reveal failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"credential_id", credID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	plain, err := s.smtpCipher.Open(cred.PasswordEnc, []byte(cred.Username))
	if err != nil {
		s.log.Error("decrypt smtp credential failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"credential_id", credID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	principal := PrincipalFromContext(ctx)
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}
	targetType := "smtp_credential"
	targetID := strconv.FormatInt(cred.ID, 10)
	if _, err := s.store.InsertAudit(ctx, store.InsertAuditParams{
		At:          s.clk.Now().UTC(),
		ActorType:   actorType,
		ActorUserID: actorUserID,
		ActorLabel:  actorLabel,
		ProjectID:   &pctx.Project.ID,
		Action:      "smtp_credential.reveal",
		TargetType:  &targetType,
		TargetID:    &targetID,
		IP:          ipPtr,
		Metadata: map[string]any{
			"label":    cred.Label,
			"username": cred.Username,
		},
	}); err != nil {
		s.log.Error("write smtp_credential.reveal audit failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"credential_id", cred.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, revealSMTPCredentialResponse{
		Password: string(plain),
	})
}

func (s *Server) handleRotateSMTPCredential(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}
	if s.smtpCipher == nil {
		s.log.Error("smtp credential cipher not initialized",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	credID, err := parsePositiveInt64Param(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid credential id", nil)
		return
	}

	password, err := randomString(smtpPasswordAlphabet, smtpPasswordLen)
	if err != nil {
		s.log.Error("generate rotated smtp password failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"credential_id", credID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	var rotated store.SMTPCredential
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		existing, err := tx.GetSMTPCredentialByID(ctx, pctx.Project.ID, credID)
		if err != nil {
			return err
		}

		sealed, err := s.smtpCipher.Seal([]byte(password), []byte(existing.Username))
		if err != nil {
			return fmt.Errorf("seal rotated password: %w", err)
		}

		u, err := tx.UpdateSMTPCredentialPassword(ctx, pctx.Project.ID, credID, sealed)
		if err != nil {
			return err
		}

		targetType := "smtp_credential"
		targetID := strconv.FormatInt(u.ID, 10)
		if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			ProjectID:   &pctx.Project.ID,
			Action:      "smtp_credential.rotate",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata: map[string]any{
				"label":    u.Label,
				"username": u.Username,
			},
		}); err != nil {
			return err
		}

		rotated = u
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "credential not found", nil)
			return
		}
		s.log.Error("rotate smtp credential failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"credential_id", credID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, createSMTPCredentialResponse{
		Credential: toSMTPCredentialResponse(rotated),
		Password:   password,
	})
}

func (s *Server) handleUpdateSMTPCredential(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	credID, err := parsePositiveInt64Param(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid credential id", nil)
		return
	}

	var req patchSMTPCredentialRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	var nextLabelPtr *string
	if req.Label != nil {
		trimmed := strings.TrimSpace(*req.Label)
		if errMsg := validateSMTPCredentialLabel(trimmed); errMsg != "" {
			writeValidationError(w, map[string]string{"label": errMsg})
			return
		}
		nextLabelPtr = &trimmed
	}

	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	actorType, actorUserID, actorLabel := auditActorForPrincipal(principal)
	var ipPtr *string
	if ip := ClientIPFromContext(ctx); ip != "" {
		ipPtr = &ip
	}

	var updated store.SMTPCredential
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		existing, err := tx.GetSMTPCredentialByID(ctx, pctx.Project.ID, credID)
		if err != nil {
			return err
		}

		nextLabel := existing.Label
		if nextLabelPtr != nil {
			nextLabel = *nextLabelPtr
		}

		nextDisabledAt := existing.DisabledAt
		if req.Disabled != nil {
			if *req.Disabled {
				if nextDisabledAt == nil {
					t := now
					nextDisabledAt = &t
				}
			} else {
				nextDisabledAt = nil
			}
		}

		u, err := tx.UpdateSMTPCredential(ctx, pctx.Project.ID, credID, nextLabel, nextDisabledAt)
		if err != nil {
			return err
		}

		if req.Label != nil || req.Disabled != nil {
			targetType := "smtp_credential"
			targetID := strconv.FormatInt(u.ID, 10)
			if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
				At:          now,
				ActorType:   actorType,
				ActorUserID: actorUserID,
				ActorLabel:  actorLabel,
				ProjectID:   &pctx.Project.ID,
				Action:      "smtp_credential.update",
				TargetType:  &targetType,
				TargetID:    &targetID,
				IP:          ipPtr,
				Metadata: map[string]any{
					"label":    u.Label,
					"username": u.Username,
					"disabled": u.DisabledAt != nil,
				},
			}); err != nil {
				return err
			}
		}

		updated = u
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "credential not found", nil)
			return
		}
		s.log.Error("update smtp credential failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"credential_id", credID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	writeJSON(w, http.StatusOK, toSMTPCredentialResponse(updated))
}

func (s *Server) handleDeleteSMTPCredential(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pctx, ok := ProjectFromContext(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	credID, err := parsePositiveInt64Param(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid credential id", nil)
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
		existing, err := tx.GetSMTPCredentialByID(ctx, pctx.Project.ID, credID)
		if err != nil {
			return err
		}
		if err := tx.DeleteSMTPCredential(ctx, pctx.Project.ID, credID); err != nil {
			return err
		}

		targetType := "smtp_credential"
		targetID := strconv.FormatInt(existing.ID, 10)
		_, err = tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  actorLabel,
			ProjectID:   &pctx.Project.ID,
			Action:      "smtp_credential.delete",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata: map[string]any{
				"label":    existing.Label,
				"username": existing.Username,
			},
		})
		return err
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "credential not found", nil)
			return
		}
		s.log.Error("delete smtp credential failed",
			"request_id", RequestIDFromContext(ctx),
			"project_id", pctx.Project.ID,
			"credential_id", credID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func validateSMTPCredentialLabel(label string) string {
	n := utf8.RuneCountInString(label)
	if n < 1 || n > maxSMTPCredentialLabelRunes {
		return "must be between 1 and 80 characters"
	}
	return ""
}

func toSMTPCredentialResponse(c store.SMTPCredential) smtpCredentialResponse {
	return smtpCredentialResponse{
		ID:         c.ID,
		Label:      c.Label,
		Username:   c.Username,
		CreatedAt:  Timestamp(c.CreatedAt),
		LastUsedAt: formatTimestampPtr(c.LastUsedAt),
		Disabled:   c.DisabledAt != nil,
	}
}

func randomString(alphabet string, length int) (string, error) {
	maxIdx := big.NewInt(int64(len(alphabet)))
	buf := make([]byte, length)
	for i := range buf {
		n, err := rand.Int(rand.Reader, maxIdx)
		if err != nil {
			return "", fmt.Errorf("random string: %w", err)
		}
		buf[i] = alphabet[n.Int64()]
	}
	return string(buf), nil
}
