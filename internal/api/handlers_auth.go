package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
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

const (
	maxUserNameRunes     = 80
	maxInstanceNameRunes = 80
	maxUserAgentLen      = 512
	settingSetupHashKey  = "setup_token_hash"
	settingSetupDoneKey  = "setup_completed_at"
	settingInstanceName  = "instance_name"
)

var (
	errSetupAlreadyDone = errors.New("setup already completed")
	errResetTokenGone   = errors.New("password reset token gone")
)

type setupRequest struct {
	SetupToken   string  `json:"setup_token"`
	Email        string  `json:"email"`
	Name         string  `json:"name"`
	Password     string  `json:"password"`
	InstanceName *string `json:"instance_name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type patchAuthMeRequest struct {
	Name            *string `json:"name"`
	CurrentPassword *string `json:"current_password"`
	NewPassword     *string `json:"new_password"`
}

type passwordResetRequest struct {
	Password string `json:"password"`
}

type sessionResponse struct {
	ID         string    `json:"id"`
	CreatedAt  Timestamp `json:"created_at"`
	LastSeenAt Timestamp `json:"last_seen_at"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	Current    bool      `json:"current"`
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req setupRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// FR-040 AC: after setup, POST /api/v1/setup returns 409.
	userCount, err := s.store.CountUsers(ctx)
	if err != nil {
		s.log.Error("setup count users failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	if userCount > 0 {
		writeError(w, http.StatusConflict, ErrCodeConflict, "setup has already been completed", nil)
		return
	}

	// FR-040 AC: setup without a valid token returns 403.
	if !s.verifySetupToken(ctx, req.SetupToken) {
		writeError(w, http.StatusForbidden, ErrCodeForbidden, "invalid setup token", nil)
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

	if err := auth.ValidatePassword(req.Password); err != nil {
		details["password"] = "must be between 10 and 256 characters"
	}

	var instanceName string
	if req.InstanceName != nil {
		trimmed := strings.TrimSpace(*req.InstanceName)
		if trimmed != "" {
			if n := utf8.RuneCountInString(trimmed); n > maxInstanceNameRunes {
				details["instance_name"] = "must be at most 80 characters"
			} else {
				instanceName = trimmed
			}
		}
	}

	if len(details) > 0 {
		writeValidationError(w, details)
		return
	}

	passwordHash, err := s.hasher.Hash(ctx, req.Password)
	if err != nil {
		s.log.Error("setup password hash failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	now := s.clk.Now().UTC()
	clientIP := ClientIPFromContext(ctx)
	userAgent := truncateString(strings.TrimSpace(r.UserAgent()), maxUserAgentLen)

	var (
		createdUser store.User
		rawToken    string
	)
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		n, err := tx.CountUsers(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return errSetupAlreadyDone
		}

		user, err := tx.CreateUser(ctx, store.CreateUserParams{
			Email:             email,
			Name:              name,
			PasswordHash:      &passwordHash,
			IsAdmin:           true,
			CanCreateProjects: true,
			LastLoginAt:       &now,
			Now:               now,
		})
		if err != nil {
			return err
		}

		if instanceName != "" {
			valJSON, err := json.Marshal(instanceName)
			if err != nil {
				return err
			}
			if err := tx.SetSetting(ctx, settingInstanceName, string(valJSON), now); err != nil {
				return err
			}
		}

		doneJSON, err := json.Marshal(now.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		if err := tx.SetSetting(ctx, settingSetupDoneKey, string(doneJSON), now); err != nil {
			return err
		}
		if err := tx.DeleteSetting(ctx, settingSetupHashKey); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}

		tok, err := s.createSessionTx(ctx, tx, user.ID, clientIP, userAgent, now)
		if err != nil {
			return err
		}

		targetType := "user"
		targetID := strconv.FormatInt(user.ID, 10)
		var ipPtr *string
		if clientIP != "" {
			ipPtr = &clientIP
		}
		if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   store.AuditActorUser,
			ActorUserID: &user.ID,
			ActorLabel:  &user.Email,
			Action:      "setup.complete",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata:    map[string]any{"method": "wizard"},
		}); err != nil {
			return err
		}

		createdUser = user
		rawToken = tok
		return nil
	})
	if err != nil {
		if errors.Is(err, errSetupAlreadyDone) || errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, ErrCodeConflict, "setup has already been completed", nil)
			return
		}
		s.log.Error("setup transaction failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	http.SetCookie(w, auth.NewSessionCookie(rawToken, s.cfg.BaseURL, s.cfg.SessionTTL))
	writeJSON(w, http.StatusCreated, buildAuthUser(createdUser))
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	normEmail := auth.NormalizeEmail(req.Email)
	clientIP := ClientIPFromContext(ctx)
	pairKey := clientIP + "\x00" + normEmail

	// SPEC 12.4: HTTP login rate limit per (IP, email) and 5x per IP.
	if retryAfter, blocked := s.checkLoginRateLimit(pairKey, clientIP); blocked {
		sec := ratelimit.RetryAfterSeconds(retryAfter)
		w.Header().Set("Retry-After", strconv.Itoa(sec))
		writeError(w, http.StatusTooManyRequests, ErrCodeRateLimited, "too many login attempts, try again later", map[string]string{
			"retry_after": strconv.Itoa(sec),
		})
		return
	}

	now := s.clk.Now().UTC()
	var ipPtr *string
	if clientIP != "" {
		ipPtr = &clientIP
	}

	failLogin := func(user *store.User, reason string) {
		s.recordLoginFailure(pairKey, clientIP)
		actorType := store.AuditActorSystem
		var actorUserID *int64
		actorLabel := normEmail
		if user != nil {
			actorType = store.AuditActorUser
			actorUserID = &user.ID
			actorLabel = user.Email
		}
		var labelPtr *string
		if actorLabel != "" {
			labelPtr = &actorLabel
		}
		if _, err := s.store.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   actorType,
			ActorUserID: actorUserID,
			ActorLabel:  labelPtr,
			Action:      "auth.login_failed",
			IP:          ipPtr,
			Metadata:    map[string]any{"reason": reason},
		}); err != nil {
			s.log.Warn("failed to write auth.login_failed audit entry", "error", err)
		}
		writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "invalid email or password", nil)
	}

	if normEmail == "" || req.Password == "" {
		_ = s.hasher.VerifyDummy(ctx, req.Password)
		failLogin(nil, "missing_credentials")
		return
	}

	user, err := s.store.GetUserByEmail(ctx, normEmail)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// FR-043: verify against dummy hash when user does not exist.
			_ = s.hasher.VerifyDummy(ctx, req.Password)
			failLogin(nil, "unknown_user")
			return
		}
		s.log.Error("login lookup user failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	// FR-043: disabled users and users without a password take the dummy-hash path.
	if user.DisabledAt != nil {
		_ = s.hasher.VerifyDummy(ctx, req.Password)
		failLogin(&user, "user_disabled")
		return
	}
	if user.PasswordHash == nil || *user.PasswordHash == "" {
		_ = s.hasher.VerifyDummy(ctx, req.Password)
		failLogin(&user, "password_not_set")
		return
	}

	ok, needsRehash, err := s.hasher.Verify(ctx, req.Password, *user.PasswordHash)
	if err != nil {
		s.log.Error("login password verify failed", "request_id", RequestIDFromContext(ctx), "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}
	if !ok {
		failLogin(&user, "invalid_password")
		return
	}

	var rehashed string
	if needsRehash {
		if h, hashErr := s.hasher.Hash(ctx, req.Password); hashErr == nil {
			rehashed = h
		}
	}

	userAgent := truncateString(strings.TrimSpace(r.UserAgent()), maxUserAgentLen)
	var rawToken string
	if err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		if rehashed != "" {
			if err := tx.UpdateUserPassword(ctx, user.ID, rehashed, now); err != nil {
				return err
			}
		}
		if err := tx.UpdateUserLastLogin(ctx, user.ID, now); err != nil {
			return err
		}
		tok, err := s.createSessionTx(ctx, tx, user.ID, clientIP, userAgent, now)
		if err != nil {
			return err
		}
		targetType := "user"
		targetID := strconv.FormatInt(user.ID, 10)
		if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   store.AuditActorUser,
			ActorUserID: &user.ID,
			ActorLabel:  &user.Email,
			Action:      "auth.login",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
		}); err != nil {
			return err
		}
		rawToken = tok
		return nil
	}); err != nil {
		s.log.Error("login create session failed", "request_id", RequestIDFromContext(ctx), "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	http.SetCookie(w, auth.NewSessionCookie(rawToken, s.cfg.BaseURL, s.cfg.SessionTTL))
	writeJSON(w, http.StatusOK, buildAuthUser(user))
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	principal := PrincipalFromContext(ctx)
	now := s.clk.Now().UTC()
	clientIP := ClientIPFromContext(ctx)
	var ipPtr *string
	if clientIP != "" {
		ipPtr = &clientIP
	}

	if err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.DeleteSessionByID(ctx, principal.SessionID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		_, err := tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   store.AuditActorUser,
			ActorUserID: &principal.UserID,
			ActorLabel:  &principal.Email,
			Action:      "auth.logout",
			IP:          ipPtr,
		})
		return err
	}); err != nil {
		s.log.Error("logout failed", "request_id", RequestIDFromContext(ctx), "user_id", principal.UserID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	s.clearAdminAccessForSession(principal.SessionID)
	http.SetCookie(w, auth.ClearSessionCookie(s.cfg.BaseURL))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateAuthMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	principal := PrincipalFromContext(ctx)

	var req patchAuthMeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	details := make(map[string]string)
	var nextName *string
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if n := utf8.RuneCountInString(trimmed); n < 1 || n > maxUserNameRunes {
			details["name"] = "must be between 1 and 80 characters"
		} else {
			nextName = &trimmed
		}
	}

	changingPassword := req.NewPassword != nil || req.CurrentPassword != nil
	if changingPassword {
		if req.CurrentPassword == nil || *req.CurrentPassword == "" {
			details["current_password"] = "current password is required"
		}
		if req.NewPassword == nil || *req.NewPassword == "" {
			details["new_password"] = "new password is required"
		} else if err := auth.ValidatePassword(*req.NewPassword); err != nil {
			details["new_password"] = "must be between 10 and 256 characters"
		}
	}

	if len(details) > 0 {
		writeValidationError(w, details)
		return
	}

	user, err := s.store.GetUserByID(ctx, principal.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, "authentication required", nil)
			return
		}
		s.log.Error("patch auth/me lookup user failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	var newPasswordHash string
	if changingPassword {
		if user.PasswordHash == nil || *user.PasswordHash == "" {
			_ = s.hasher.VerifyDummy(ctx, *req.CurrentPassword)
			writeValidationError(w, map[string]string{"current_password": "current password is incorrect"})
			return
		}
		ok, _, err := s.hasher.Verify(ctx, *req.CurrentPassword, *user.PasswordHash)
		if err != nil {
			s.log.Error("patch auth/me verify current password failed", "request_id", RequestIDFromContext(ctx), "error", err)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
		if !ok {
			writeValidationError(w, map[string]string{"current_password": "current password is incorrect"})
			return
		}
		h, err := s.hasher.Hash(ctx, *req.NewPassword)
		if err != nil {
			s.log.Error("patch auth/me hash new password failed", "request_id", RequestIDFromContext(ctx), "error", err)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
		newPasswordHash = h
	}

	now := s.clk.Now().UTC()
	clientIP := ClientIPFromContext(ctx)
	var ipPtr *string
	if clientIP != "" {
		ipPtr = &clientIP
	}

	updated := user
	if err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		if nextName != nil {
			u, err := tx.UpdateUserName(ctx, user.ID, *nextName, now)
			if err != nil {
				return err
			}
			updated = u
		}
		if changingPassword {
			if err := tx.UpdateUserPassword(ctx, user.ID, newPasswordHash, now); err != nil {
				return err
			}
			// FR-044: password change deletes all other sessions of the user.
			if _, err := tx.DeleteOtherSessionsForUser(ctx, user.ID, principal.SessionID); err != nil {
				return err
			}
			targetType := "user"
			targetID := strconv.FormatInt(user.ID, 10)
			if _, err := tx.InsertAudit(ctx, store.InsertAuditParams{
				At:          now,
				ActorType:   store.AuditActorUser,
				ActorUserID: &user.ID,
				ActorLabel:  &user.Email,
				Action:      "user.password_change",
				TargetType:  &targetType,
				TargetID:    &targetID,
				IP:          ipPtr,
				Metadata:    map[string]any{"via": "self"},
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		s.log.Error("patch auth/me update failed", "request_id", RequestIDFromContext(ctx), "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	writeJSON(w, http.StatusOK, buildAuthUser(updated))
}

func (s *Server) handleListAuthSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	principal := PrincipalFromContext(ctx)

	sessions, err := s.store.ListSessionsByUser(ctx, principal.UserID)
	if err != nil {
		s.log.Error("list sessions failed", "request_id", RequestIDFromContext(ctx), "user_id", principal.UserID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	now := s.clk.Now().UTC()
	out := make([]sessionResponse, 0, len(sessions))
	for _, sess := range sessions {
		if auth.IsSessionExpired(now, sess.LastSeenAt, sess.ExpiresAt, s.cfg.SessionIdleTimeout) {
			continue
		}
		out = append(out, sessionResponse{
			ID:         sess.ID,
			CreatedAt:  Timestamp(sess.CreatedAt),
			LastSeenAt: Timestamp(sess.LastSeenAt),
			IP:         sess.IP,
			UserAgent:  sess.UserAgent,
			Current:    sess.ID == principal.SessionID,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteAuthSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	principal := PrincipalFromContext(ctx)
	targetID := strings.TrimSpace(r.PathValue("id"))
	if targetID == "" {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "session not found", nil)
		return
	}

	if err := s.store.DeleteSessionByIDForUser(ctx, targetID, principal.UserID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "session not found", nil)
			return
		}
		s.log.Error("delete session failed", "request_id", RequestIDFromContext(ctx), "user_id", principal.UserID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	s.clearAdminAccessForSession(targetID)
	if targetID == principal.SessionID {
		http.SetCookie(w, auth.ClearSessionCookie(s.cfg.BaseURL))
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePasswordReset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clientIP := ClientIPFromContext(ctx)

	// SPEC 12.4: password reset is rate-limited to 20 requests per 15 min per IP.
	if ok, retryAfter := s.resetIPLimiter.Allow(clientIP); !ok {
		sec := ratelimit.RetryAfterSeconds(retryAfter)
		w.Header().Set("Retry-After", strconv.Itoa(sec))
		writeError(w, http.StatusTooManyRequests, ErrCodeRateLimited, "too many password reset attempts, try again later", map[string]string{
			"retry_after": strconv.Itoa(sec),
		})
		return
	}

	var req passwordResetRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := auth.ValidatePassword(req.Password); err != nil {
		writeValidationError(w, map[string]string{
			"password": "must be between 10 and 256 characters",
		})
		return
	}

	rawToken := strings.TrimSpace(r.PathValue("token"))
	if rawToken == "" {
		writeError(w, http.StatusGone, ErrCodeGone, "password reset link is invalid or has expired", nil)
		return
	}

	rec, err := s.store.GetPasswordResetByTokenHash(ctx, auth.HashToken(rawToken))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusGone, ErrCodeGone, "password reset link is invalid or has expired", nil)
			return
		}
		s.log.Error("password reset lookup failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	now := s.clk.Now().UTC()
	if rec.UsedAt != nil || !now.Before(rec.ExpiresAt) {
		writeError(w, http.StatusGone, ErrCodeGone, "password reset link is invalid or has expired", nil)
		return
	}

	user, err := s.store.GetUserByID(ctx, rec.UserID)
	if err != nil || user.DisabledAt != nil {
		writeError(w, http.StatusGone, ErrCodeGone, "password reset link is invalid or has expired", nil)
		return
	}

	newHash, err := s.hasher.Hash(ctx, req.Password)
	if err != nil {
		s.log.Error("password reset hash failed", "request_id", RequestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	var ipPtr *string
	if clientIP != "" {
		ipPtr = &clientIP
	}

	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		consumed, err := tx.ConsumePasswordReset(ctx, rec.ID, now)
		if err != nil {
			return err
		}
		if !consumed {
			return errResetTokenGone
		}
		if err := tx.UpdateUserPassword(ctx, user.ID, newHash, now); err != nil {
			return err
		}
		// FR-046: resetting password deletes all of the user's sessions.
		if _, err := tx.DeleteAllSessionsForUser(ctx, user.ID); err != nil {
			return err
		}
		targetType := "user"
		targetID := strconv.FormatInt(user.ID, 10)
		_, err = tx.InsertAudit(ctx, store.InsertAuditParams{
			At:          now,
			ActorType:   store.AuditActorUser,
			ActorUserID: &user.ID,
			ActorLabel:  &user.Email,
			Action:      "user.password_change",
			TargetType:  &targetType,
			TargetID:    &targetID,
			IP:          ipPtr,
			Metadata:    map[string]any{"via": "password_reset"},
		})
		return err
	})
	if err != nil {
		if errors.Is(err, errResetTokenGone) {
			writeError(w, http.StatusGone, ErrCodeGone, "password reset link is invalid or has expired", nil)
			return
		}
		s.log.Error("password reset commit failed", "request_id", RequestIDFromContext(ctx), "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) verifySetupToken(ctx context.Context, rawToken string) bool {
	trimmed := strings.TrimSpace(rawToken)
	if trimmed == "" {
		return false
	}
	setting, err := s.store.GetSetting(ctx, settingSetupHashKey)
	if err != nil {
		return false
	}
	var expectedHash string
	if err := json.Unmarshal([]byte(setting.Value), &expectedHash); err != nil || expectedHash == "" {
		return false
	}
	gotHash := auth.HashToken(trimmed)
	return subtle.ConstantTimeCompare([]byte(gotHash), []byte(expectedHash)) == 1
}

func (s *Server) createSessionTx(ctx context.Context, tx *store.Tx, userID int64, ip, userAgent string, now time.Time) (string, error) {
	rawToken, err := auth.GenerateToken()
	if err != nil {
		return "", err
	}
	csrfToken, err := auth.GenerateToken()
	if err != nil {
		return "", err
	}
	if _, err := tx.CreateSession(ctx, store.CreateSessionParams{
		ID:        auth.HashToken(rawToken),
		UserID:    userID,
		CSRFToken: csrfToken,
		IP:        ip,
		UserAgent: userAgent,
		Now:       now,
		ExpiresAt: now.Add(s.cfg.SessionTTL),
	}); err != nil {
		return "", err
	}
	return rawToken, nil
}

func (s *Server) checkLoginRateLimit(pairKey, ip string) (time.Duration, bool) {
	okPair, retryPair := s.loginKeyLimiter.Check(pairKey)
	okIP, retryIP := s.loginIPLimiter.Check(ip)
	if okPair && okIP {
		return 0, false
	}
	return max(retryPair, retryIP), true
}

func (s *Server) recordLoginFailure(pairKey, ip string) {
	s.loginKeyLimiter.Record(pairKey)
	s.loginIPLimiter.Record(ip)
}

func buildAuthUser(u store.User) authUserResponse {
	return authUserResponse{
		ID:                u.ID,
		Email:             u.Email,
		Name:              u.Name,
		IsAdmin:           u.IsAdmin,
		CanCreateProjects: u.CanCreateProjects,
	}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
