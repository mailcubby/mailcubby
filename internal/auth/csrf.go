package auth

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
)

// CSRFHeaderName is the HTTP header carrying the session CSRF token (SPEC FR-045).
const CSRFHeaderName = "X-CSRF-Token"

const maxEmailLen = 254

// IsUnsafeMethod reports whether an HTTP method mutates state and therefore
// requires Origin (and CSRF for cookie auth) validation (SPEC FR-045).
func IsUnsafeMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// CheckOrigin validates that the request's Origin header (or Referer when
// Origin is absent) has the exact same scheme and host/port as baseURL (SPEC FR-045).
func CheckOrigin(r *http.Request, baseURL string) bool {
	baseOrigin, ok := canonicalOrigin(baseURL)
	if !ok {
		return false
	}

	raw := strings.TrimSpace(r.Header.Get("Origin"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Referer"))
	}
	if raw == "" || strings.EqualFold(raw, "null") {
		return false
	}

	reqOrigin, ok := canonicalOrigin(raw)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(reqOrigin), []byte(baseOrigin)) == 1
}

// CheckCSRFToken compares got against expected in constant time (SPEC FR-045).
func CheckCSRFToken(got, expected string) bool {
	if got == "" || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

// NormalizeEmail trims whitespace and lowercases an email address for lookup
// and rate-limit keying.
func NormalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// ValidateEmail checks that raw is a valid, bare email address and returns its
// trimmed form.
func ValidateEmail(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > maxEmailLen {
		return "", errors.New("must be a valid email address")
	}
	if strings.ContainsAny(trimmed, " \t\r\n<>") {
		return "", errors.New("must be a valid email address")
	}
	addr, err := mail.ParseAddress(trimmed)
	if err != nil || addr.Name != "" || addr.Address != trimmed {
		return "", errors.New("must be a valid email address")
	}
	local, domain, ok := strings.Cut(trimmed, "@")
	if !ok || local == "" || domain == "" || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", errors.New("must be a valid email address")
	}
	return trimmed, nil
}

func canonicalOrigin(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", false
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		return scheme + "://" + host + ":" + port, true
	}
	return scheme + "://" + host, true
}
