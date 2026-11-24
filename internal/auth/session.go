package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	// SessionCookieName is the HTTP cookie holding the raw session token (SPEC FR-044).
	SessionCookieName = "mailcubby_session"
	// TokenByteLen is the entropy length (256 bits) for session, CSRF, setup, and reset tokens.
	TokenByteLen = 32
	// SessionTouchInterval bounds how often sessions.last_seen_at is updated (SPEC FR-044).
	SessionTouchInterval = time.Minute
	// PasswordResetTTL is the lifetime of single-use password reset tokens (SPEC FR-046).
	PasswordResetTTL = time.Hour
)

// GenerateToken returns a cryptographically random 32-byte token encoded as
// unpadded base64url (SPEC FR-044, 12.3).
func GenerateToken() (string, error) {
	b := make([]byte, TokenByteLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken returns the lowercase hex-encoded SHA-256 digest of token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// IsHTTPSBaseURL reports whether baseURL uses the https scheme.
func IsHTTPSBaseURL(baseURL string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(baseURL)), "https://")
}

// NewSessionCookie builds the session cookie with HttpOnly, SameSite=Lax, Path=/,
// and Secure when baseURL is HTTPS (SPEC FR-044).
func NewSessionCookie(rawToken, baseURL string, ttl time.Duration) *http.Cookie {
	maxAge := int(ttl.Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	//nolint:gosec // G124: Secure is derived from MAILCUBBY_BASE_URL scheme per SPEC FR-044.
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    rawToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   IsHTTPSBaseURL(baseURL),
		MaxAge:   maxAge,
	}
}

// ClearSessionCookie builds an expired cookie instructing browsers to delete
// the session cookie (SPEC FR-044).
func ClearSessionCookie(baseURL string) *http.Cookie {
	//nolint:gosec // G124: Secure is derived from MAILCUBBY_BASE_URL scheme per SPEC FR-044.
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   IsHTTPSBaseURL(baseURL),
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	}
}

// IsSessionExpired reports whether a session has exceeded its absolute lifetime
// or idle timeout at now (SPEC FR-044).
func IsSessionExpired(now, lastSeenAt, expiresAt time.Time, idleTimeout time.Duration) bool {
	if !now.Before(expiresAt) {
		return true
	}
	if idleTimeout > 0 && now.Sub(lastSeenAt) > idleTimeout {
		return true
	}
	return false
}

// NeedsSessionTouch reports whether last_seen_at should be refreshed (at most
// once per minute per SPEC FR-044).
func NeedsSessionTouch(now, lastSeenAt time.Time) bool {
	return now.Sub(lastSeenAt) >= SessionTouchInterval
}
