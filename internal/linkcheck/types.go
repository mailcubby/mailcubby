// Package linkcheck provides offline email link extraction and SSRF-hardened
// active HTTP link checking following ADR 0010.
package linkcheck

import (
	"context"
	"crypto/x509"
	"io"
	"net/netip"
	"time"
)

// Source identifies where in a message a link was discovered.
type Source string

const (
	// SourceHTML indicates the link appeared in an HTML <a href> or <area href>.
	SourceHTML Source = "html"
	// SourceText indicates the link appeared in a plain-text body part.
	SourceText Source = "text"
	// SourceHeader indicates the link appeared in a List-Unsubscribe header.
	SourceHeader Source = "header"
)

// Warning identifies a static offline warning about an extracted link (ADR 0010).
type Warning string

const (
	// WarningJavaScriptURL flags a javascript: link.
	WarningJavaScriptURL Warning = "javascript_url"
	// WarningDataURL flags a data: URI link.
	WarningDataURL Warning = "data_url"
	// WarningCredentialsInURL flags embedded userinfo (user:pass@host).
	WarningCredentialsInURL Warning = "credentials_in_url"
	// WarningHTTPInsecure flags a plaintext http:// link.
	WarningHTTPInsecure Warning = "http_insecure"
	// WarningMissingHref flags an <a> or <area> element with a missing or empty href.
	WarningMissingHref Warning = "missing_href"
)

// Status classifies the outcome of actively checking one link.
type Status string

const (
	// StatusOK indicates an HTTP 2xx response with no redirects.
	StatusOK Status = "ok"
	// StatusRedirect indicates an HTTP 2xx response after redirects, or a final 3xx response.
	StatusRedirect Status = "redirect"
	// StatusClientError indicates an HTTP 4xx response.
	StatusClientError Status = "client_error"
	// StatusServerError indicates an HTTP 5xx response.
	StatusServerError Status = "server_error"
	// StatusBlocked indicates the target was rejected by SSRF or URL validation policy.
	StatusBlocked Status = "blocked"
	// StatusTimeout indicates the check timed out.
	StatusTimeout Status = "timeout"
	// StatusDNSError indicates DNS resolution failed.
	StatusDNSError Status = "dns_error"
	// StatusTLSError indicates TLS handshake or certificate verification failed.
	StatusTLSError Status = "tls_error"
	// StatusNetworkError indicates another transport-level failure.
	StatusNetworkError Status = "network_error"
	// StatusTooManyRedirects indicates the redirect limit (5) was exceeded.
	StatusTooManyRedirects Status = "too_many_redirects"
	// StatusSkippedLimit indicates the URL exceeded MAILCUBBY_LINK_CHECK_MAX_LINKS.
	StatusSkippedLimit Status = "skipped_limit"
)

// Machine-readable classified error codes returned in CheckResult.ErrorCode.
// These codes never contain raw URLs, IPs, or raw *url.Error strings (ADR 0010).
const (
	ErrCodeBlockedScheme    = "blocked_scheme"
	ErrCodeBlockedHost      = "blocked_host"
	ErrCodeBlockedIP        = "blocked_ip"
	ErrCodeBlockedPort      = "blocked_port"
	ErrCodeCredentialsInURL = "credentials_in_url"
	ErrCodeInvalidURL       = "invalid_url"
	ErrCodeTooManyRedirects = "too_many_redirects"
	ErrCodeTimeout          = "timeout"
	ErrCodeDNSError         = "dns_error"
	ErrCodeTLSError         = "tls_error"
	ErrCodeNetworkError     = "network_error"
	ErrCodeSkippedLimit     = "skipped_limit"
)

// ExtractedLink represents one deduplicated link extracted offline from a message.
type ExtractedLink struct {
	URL       string    `json:"url"`
	Text      string    `json:"text"`
	Sources   []Source  `json:"sources"`
	Count     int       `json:"count"`
	Scheme    string    `json:"scheme"`
	Checkable bool      `json:"checkable"`
	Warnings  []Warning `json:"warnings"`
}

// ExtractResult holds all deduplicated links extracted offline from a message.
type ExtractResult struct {
	Links          []ExtractedLink `json:"links"`
	Total          int             `json:"total"`
	CheckableCount int             `json:"checkable_count"`
	Truncated      bool            `json:"truncated"`
}

// CheckResult holds the active check outcome for a single URL.
type CheckResult struct {
	URL           string  `json:"url"`
	Status        Status  `json:"status"`
	StatusCode    *int    `json:"status_code"`
	FinalURL      *string `json:"final_url"`
	RedirectCount int     `json:"redirect_count"`
	DurationMS    int64   `json:"duration_ms"`
	Cached        bool    `json:"cached"`
	ErrorCode     *string `json:"error_code"`
	Error         *string `json:"error"`
}

// CheckSummary aggregates counts across all results in a CheckReport.
type CheckSummary struct {
	Total    int `json:"total"`
	OK       int `json:"ok"`
	Redirect int `json:"redirect"`
	Broken   int `json:"broken"`
	Blocked  int `json:"blocked"`
	Error    int `json:"error"`
	Skipped  int `json:"skipped"`
}

// CheckReport is the result of checking a message's links.
type CheckReport struct {
	CheckedAt time.Time
	Summary   CheckSummary
	Results   []CheckResult
}

// BlobOpener opens a decompressed message blob by its relative path.
type BlobOpener interface {
	Open(relPath string) (io.ReadCloser, error)
}

// Resolver resolves hostnames to IP addresses inside DialContext.
// Defaults to net.DefaultResolver; tests inject a fake implementation to simulate
// DNS rebinding and multi-record responses without external network access.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Config configures a Checker instance (ADR 0010).
type Config struct {
	// Timeout is the per-URL check timeout (default 5s, capped at 30s).
	Timeout time.Duration
	// OverallTimeout caps the total duration of a single Check call (default 30s).
	OverallTimeout time.Duration
	// MaxLinks caps the number of unique URLs checked per request (default 50, max 200).
	MaxLinks int
	// AllowedPorts lists TCP ports allowed for outbound checks (default 80, 443).
	AllowedPorts []int
	// AllowCIDRs lists explicit CIDR ranges exempted from the private/local IP denylist.
	AllowCIDRs []netip.Prefix
	// CacheSize is the LRU capacity in {projectID, msgID} entries (default 64, negative disables).
	CacheSize int
	// CacheTTL is the per-URL cache TTL (default 5m).
	CacheTTL time.Duration
	// PerRequestConcurrency caps concurrent checks within one request (default 5).
	PerRequestConcurrency int
	// GlobalConcurrency caps concurrent checks across all requests (default 16).
	GlobalConcurrency int
	// Version is included in the User-Agent header (Mailcubby-LinkChecker/<version>).
	Version string
	// Resolver overrides DNS resolution (defaults to net.DefaultResolver).
	Resolver Resolver
	// RootCAs optionally supplies custom trusted root CAs for TLS verification (used in tests).
	// InsecureSkipVerify is never enabled.
	RootCAs *x509.CertPool
}
