package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	// APITokenPrefix starts every Mailcubby personal API token (SPEC FR-065).
	APITokenPrefix = "mailcubby_"
	// APITokenBodyLen is the number of base62 characters following APITokenPrefix.
	APITokenBodyLen = 40
	// APITokenPrefixLen is the number of body characters stored as the unique prefix.
	APITokenPrefixLen = 12
	// APITokenTouchInterval bounds how often api_tokens.last_used_at is updated.
	APITokenTouchInterval = time.Minute

	// APITokenScopeRead grants viewer-level read access (SPEC FR-066).
	APITokenScopeRead = "read"
	// APITokenScopeWrite grants member-level read/write access (SPEC FR-066).
	APITokenScopeWrite = "write"

	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// GenerateAPIToken creates a new personal API token (SPEC FR-065):
// raw is "mailcubby_" + 40 base62 chars, prefix is the first 12 body chars,
// and hash is the lowercase hex SHA-256 of raw.
func GenerateAPIToken() (raw, prefix, hash string, err error) {
	maxIdx := big.NewInt(int64(len(base62Alphabet)))
	var b strings.Builder
	b.Grow(len(APITokenPrefix) + APITokenBodyLen)
	b.WriteString(APITokenPrefix)
	for range APITokenBodyLen {
		n, randErr := rand.Int(rand.Reader, maxIdx)
		if randErr != nil {
			return "", "", "", fmt.Errorf("auth: generate api token: %w", randErr)
		}
		b.WriteByte(base62Alphabet[n.Int64()])
	}
	raw = b.String()
	prefix = raw[len(APITokenPrefix) : len(APITokenPrefix)+APITokenPrefixLen]
	hash = HashToken(raw)
	return raw, prefix, hash, nil
}

// ParseAPIToken validates raw as a Mailcubby API token and returns its 12-character
// lookup prefix when valid (SPEC FR-065, ARCHITECTURE 3.9).
func ParseAPIToken(raw string) (prefix string, ok bool) {
	if !strings.HasPrefix(raw, APITokenPrefix) {
		return "", false
	}
	body := raw[len(APITokenPrefix):]
	if len(body) != APITokenBodyLen {
		return "", false
	}
	for i := range len(body) {
		if !isBase62(body[i]) {
			return "", false
		}
	}
	return body[:APITokenPrefixLen], true
}

// VerifyAPITokenHash compares the SHA-256 digest of raw against expectedHash in
// constant time (SPEC 12.3, ARCHITECTURE 3.9).
func VerifyAPITokenHash(raw, expectedHash string) bool {
	if expectedHash == "" {
		return false
	}
	got := HashToken(raw)
	return subtle.ConstantTimeCompare([]byte(got), []byte(expectedHash)) == 1
}

// ValidAPITokenScope reports whether scope is one of the allowed API token scopes
// ("read" or "write") defined by SPEC FR-065.
func ValidAPITokenScope(scope string) bool {
	return scope == APITokenScopeRead || scope == APITokenScopeWrite
}

// NeedsAPITokenTouch reports whether api_tokens.last_used_at should be updated at now.
func NeedsAPITokenTouch(now time.Time, lastUsedAt *time.Time) bool {
	if lastUsedAt == nil {
		return true
	}
	return now.Sub(*lastUsedAt) >= APITokenTouchInterval
}

func isBase62(c byte) bool {
	return (c >= '0' && c <= '9') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z')
}
