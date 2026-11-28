package linkcheck

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// policyError classifies a URL or IP rejected by ADR 0010 SSRF policy.
// Its message is always a static safe string containing no URLs or IPs.
type policyError struct {
	code    string
	message string
}

func (e *policyError) Error() string {
	return e.message
}

// dnsResolveError wraps a DNS lookup error inside DialContext so error
// classification distinguishes DNS failures without logging hostnames or URLs.
type dnsResolveError struct {
	err error
}

func (e *dnsResolveError) Error() string {
	return "DNS lookup failed"
}

func (e *dnsResolveError) Unwrap() error {
	return e.err
}

var errTooManyRedirects = errors.New("too many redirects")

// blockedPrefixes lists every non-public, private, loopback, link-local, IMDS,
// CGNAT, ULA, site-local, multicast, broadcast, documentation, benchmark, and
// IPv4-embedding transition/legacy prefix blocked by ADR 0010.
var blockedPrefixes = []netip.Prefix{
	// IPv4 blocked ranges (RFC 1122, RFC 1918, RFC 3927, RFC 6598, RFC 6890, RFC 5737, RFC 2544, RFC 5771, RFC 1112)
	netip.MustParsePrefix("0.0.0.0/8"),       // Unspecified / "this network"
	netip.MustParsePrefix("10.0.0.0/8"),      // RFC 1918 private
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT / Shared Address Space (RFC 6598)
	netip.MustParsePrefix("127.0.0.0/8"),     // Loopback
	netip.MustParsePrefix("169.254.0.0/16"),  // Link-local / Cloud IMDS (169.254.169.254)
	netip.MustParsePrefix("172.16.0.0/12"),   // RFC 1918 private
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF Protocol Assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // Documentation (TEST-NET-1)
	netip.MustParsePrefix("192.88.99.0/24"),  // 6to4 Relay Anycast (deprecated)
	netip.MustParsePrefix("192.168.0.0/16"),  // RFC 1918 private
	netip.MustParsePrefix("198.18.0.0/15"),   // Benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // Documentation (TEST-NET-2)
	netip.MustParsePrefix("203.0.113.0/24"),  // Documentation (TEST-NET-3)
	netip.MustParsePrefix("224.0.0.0/4"),     // Multicast
	netip.MustParsePrefix("240.0.0.0/4"),     // Reserved + Limited Broadcast (255.255.255.255/32)

	// IPv6 blocked ranges (RFC 4291, RFC 4193, RFC 3879, RFC 6052, RFC 8215, RFC 3056, RFC 4380, RFC 2765, RFC 3849)
	netip.MustParsePrefix("::/128"),          // Unspecified
	netip.MustParsePrefix("::1/128"),         // Loopback
	netip.MustParsePrefix("::/96"),           // IPv4-compatible IPv6 (deprecated)
	netip.MustParsePrefix("::ffff:0:0/96"),   // IPv4-mapped IPv6 (also unmapped via ip.Unmap)
	netip.MustParsePrefix("::ffff:0:0:0/96"), // IPv4-translated IPv6 (SIIT)
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64 Well-Known Prefix
	netip.MustParsePrefix("64:ff9b:1::/48"),  // NAT64 Local-Use Prefix
	netip.MustParsePrefix("100::/64"),        // Discard-Only (RFC 6666)
	netip.MustParsePrefix("2001::/32"),       // Teredo tunneling
	netip.MustParsePrefix("2001:2::/48"),     // Benchmarking
	netip.MustParsePrefix("2001:10::/28"),    // ORCHID (deprecated)
	netip.MustParsePrefix("2001:20::/28"),    // ORCHIDv2
	netip.MustParsePrefix("2001:db8::/32"),   // Documentation (RFC 3849)
	netip.MustParsePrefix("2002::/16"),       // 6to4
	netip.MustParsePrefix("3fff::/20"),       // Documentation (RFC 9637)
	netip.MustParsePrefix("5f00::/16"),       // Segment Routing (RFC 9602)
	netip.MustParsePrefix("fc00::/7"),        // Unique Local Addresses (ULA)
	netip.MustParsePrefix("fe80::/10"),       // Link-local unicast
	netip.MustParsePrefix("fec0::/10"),       // Deprecated site-local
	netip.MustParsePrefix("ff00::/8"),        // Multicast
}

func normalizeHostname(host string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(host), "."))
}

func isLocalHostname(normHost string) bool {
	return normHost == "localhost" ||
		strings.HasSuffix(normHost, ".localhost") ||
		strings.HasSuffix(normHost, ".local") ||
		strings.HasSuffix(normHost, ".internal")
}

func normalizeAllowCIDRs(in []netip.Prefix) []netip.Prefix {
	if len(in) == 0 {
		return nil
	}
	out := make([]netip.Prefix, 0, len(in))
	for _, p := range in {
		if !p.IsValid() {
			continue
		}
		masked := p.Masked()
		out = append(out, masked)
		// If an operator specifies an IPv4-mapped IPv6 prefix (e.g. ::ffff:10.0.0.0/104),
		// also add its unmapped IPv4 equivalent so ip.Unmap() matches consistently.
		if masked.Addr().Is4In6() && masked.Bits() >= 96 {
			out = append(out, netip.PrefixFrom(masked.Addr().Unmap(), masked.Bits()-96).Masked())
		}
	}
	return out
}

// checkIP evaluates a single IP address against ADR 0010 SSRF rules.
// Zoned IPv6 addresses are always rejected regardless of allowCIDRs.
// All other addresses are unmapped via ip.Unmap() before allowlist and denylist checks.
func checkIP(ip netip.Addr, allowCIDRs []netip.Prefix, requireAllowlist bool) error {
	if !ip.IsValid() {
		return &policyError{code: ErrCodeBlockedIP, message: "target IP address is invalid"}
	}
	if ip.Zone() != "" {
		return &policyError{code: ErrCodeBlockedIP, message: "target IPv6 zone address is blocked"}
	}
	u := ip.Unmap()
	for _, prefix := range allowCIDRs {
		if prefix.Contains(u) {
			return nil
		}
	}
	if requireAllowlist {
		return &policyError{code: ErrCodeBlockedHost, message: "local hostname is blocked"}
	}
	if !u.IsGlobalUnicast() {
		return &policyError{code: ErrCodeBlockedIP, message: "target IP address is blocked"}
	}
	for _, blocked := range blockedPrefixes {
		if blocked.Contains(u) {
			return &policyError{code: ErrCodeBlockedIP, message: "target IP address is blocked"}
		}
	}
	return nil
}

// maxDialCandidates caps how many validated IPs are dialed sequentially for a single
// host after every IP returned by DNS has been validated against SSRF policy.
const maxDialCandidates = 8

// MaxURLBytes caps the length of a URL accepted for active checking or redirect hops.
const MaxURLBytes = 8 << 10 // 8 KiB

// validateTargetURL performs ADR 0010 URL pre-validation before any request or redirect hop.
func validateTargetURL(u *url.URL, allowedPorts map[int]struct{}, allowCIDRs []netip.Prefix) error {
	if u == nil {
		return &policyError{code: ErrCodeInvalidURL, message: "invalid URL"}
	}
	if len(u.String()) > MaxURLBytes {
		return &policyError{code: ErrCodeInvalidURL, message: "URL exceeds maximum length"}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return &policyError{code: ErrCodeBlockedScheme, message: "URL scheme is not allowed"}
	}
	if u.User != nil {
		return &policyError{code: ErrCodeCredentialsInURL, message: "URLs containing credentials are not allowed"}
	}
	host := u.Hostname()
	if host == "" {
		return &policyError{code: ErrCodeInvalidURL, message: "URL host is empty"}
	}
	if strings.Contains(host, "%") {
		return &policyError{code: ErrCodeBlockedIP, message: "target IPv6 zone address is blocked"}
	}

	port, err := resolvePort(scheme, u.Port())
	if err != nil {
		return err
	}
	if _, ok := allowedPorts[port]; !ok {
		return &policyError{code: ErrCodeBlockedPort, message: "target port is not allowed"}
	}

	normHost := normalizeHostname(host)
	if normHost == "" {
		return &policyError{code: ErrCodeInvalidURL, message: "URL host is empty"}
	}

	if ip, parseErr := netip.ParseAddr(normHost); parseErr == nil {
		return checkIP(ip, allowCIDRs, false)
	}
	if isLocalHostname(normHost) && len(allowCIDRs) == 0 {
		return &policyError{code: ErrCodeBlockedHost, message: "local hostname is blocked"}
	}
	return nil
}

func resolvePort(scheme, portStr string) (int, error) {
	if portStr == "" {
		if scheme == "https" {
			return 443, nil
		}
		return 80, nil
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		return 0, &policyError{code: ErrCodeBlockedPort, message: "target port is invalid"}
	}
	return p, nil
}

// newSafeDialContext builds an http.Transport DialContext function that eliminates
// DNS-rebinding TOCTOU windows by resolving inside DialContext, validating every
// returned IP, and dialing the validated literal IP directly so http.Transport
// preserves the original hostname for the Host header and TLS SNI (ADR 0010).
func newSafeDialContext(
	resolver Resolver,
	dialer *net.Dialer,
	allowedPorts map[int]struct{},
	allowCIDRs []netip.Prefix,
) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dialer == nil {
		dialer = &net.Dialer{}
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, &policyError{code: ErrCodeInvalidURL, message: "invalid target address"}
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return nil, &policyError{code: ErrCodeBlockedPort, message: "target port is invalid"}
		}
		if _, ok := allowedPorts[port]; !ok {
			return nil, &policyError{code: ErrCodeBlockedPort, message: "target port is not allowed"}
		}
		if strings.Contains(host, "%") {
			return nil, &policyError{code: ErrCodeBlockedIP, message: "target IPv6 zone address is blocked"}
		}

		normHost := normalizeHostname(host)
		if normHost == "" {
			return nil, &policyError{code: ErrCodeInvalidURL, message: "target host is empty"}
		}

		requireAllowlist := isLocalHostname(normHost)

		var ips []netip.Addr
		if literalIP, parseErr := netip.ParseAddr(normHost); parseErr == nil {
			ips = []netip.Addr{literalIP}
		} else {
			if requireAllowlist && len(allowCIDRs) == 0 {
				return nil, &policyError{code: ErrCodeBlockedHost, message: "local hostname is blocked"}
			}
			resolved, lookupErr := resolver.LookupNetIP(ctx, "ip", normHost)
			if lookupErr != nil {
				return nil, &dnsResolveError{err: lookupErr}
			}
			if len(resolved) == 0 {
				return nil, &dnsResolveError{err: errors.New("no addresses returned")}
			}
			ips = resolved
		}

		// Validate every resolved IP before dialing any of them (ADR 0010).
		validated := make([]netip.Addr, 0, len(ips))
		for _, ip := range ips {
			if err := checkIP(ip, allowCIDRs, requireAllowlist); err != nil {
				return nil, err
			}
			validated = append(validated, ip.Unmap())
		}

		if len(validated) > maxDialCandidates {
			validated = validated[:maxDialCandidates]
		}
		canonicalPort := strconv.Itoa(port)

		var lastErr error
		for _, ip := range validated {
			target := net.JoinHostPort(ip.String(), canonicalPort)
			conn, dialErr := dialer.DialContext(ctx, network, target)
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
			if ctx.Err() != nil {
				break
			}
		}
		return nil, lastErr
	}
}
