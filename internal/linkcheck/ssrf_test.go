package linkcheck

import (
	"errors"
	"net/netip"
	"net/url"
	"testing"
)

// FR-120, ADR 0010: verify the complete IP denylist covering IPv4 private/loopback/
// link-local/IMDS/CGNAT/multicast/broadcast/reserved/documentation/benchmark,
// IPv6 loopback/ULA/link-local/site-local/multicast/documentation, IPv4-mapped IPv6,
// IPv4-compatible, IPv4-translated, NAT64, 6to4, Teredo, and IPv6 zone IDs.
func TestCheckIPDenylistAndAllowlist(t *testing.T) {
	t.Parallel()

	blocked := []struct {
		name string
		ip   string
	}{
		// IPv4 unspecified, private, loopback, CGNAT, link-local / IMDS
		{"ipv4 unspecified", "0.0.0.0"},
		{"ipv4 this network", "0.255.255.255"},
		{"ipv4 rfc1918 10/8", "10.0.0.1"},
		{"ipv4 rfc1918 10/8 end", "10.255.255.255"},
		{"ipv4 cgnat start", "100.64.0.1"},
		{"ipv4 cgnat end", "100.127.255.254"},
		{"ipv4 loopback", "127.0.0.1"},
		{"ipv4 loopback high", "127.255.255.254"},
		{"ipv4 link-local", "169.254.0.1"},
		{"ipv4 cloud imds", "169.254.169.254"},
		{"ipv4 rfc1918 172.16/12", "172.16.0.1"},
		{"ipv4 rfc1918 172.31/12", "172.31.255.255"},
		{"ipv4 ietf protocol", "192.0.0.1"},
		{"ipv4 test-net-1", "192.0.2.1"},
		{"ipv4 6to4 relay anycast", "192.88.99.1"},
		{"ipv4 rfc1918 192.168/16", "192.168.1.1"},
		{"ipv4 benchmark", "198.18.0.1"},
		{"ipv4 test-net-2", "198.51.100.1"},
		{"ipv4 test-net-3", "203.0.113.1"},
		{"ipv4 multicast", "224.0.0.1"},
		{"ipv4 reserved", "240.0.0.1"},
		{"ipv4 limited broadcast", "255.255.255.255"},

		// IPv6 unspecified, loopback, ULA, link-local, site-local, multicast, doc, bench
		{"ipv6 unspecified", "::"},
		{"ipv6 loopback", "::1"},
		{"ipv6 ula fc00", "fc00::1"},
		{"ipv6 ula fd00", "fd12:3456:789a::1"},
		{"ipv6 link-local", "fe80::1"},
		{"ipv6 site-local deprecated", "fec0::1"},
		{"ipv6 multicast", "ff02::1"},
		{"ipv6 discard-only", "100::1"},
		{"ipv6 documentation", "2001:db8::1"},
		{"ipv6 rfc9637 documentation", "3fff::1"},
		{"ipv6 srv6 rfc9602", "5f00::1"},
		{"ipv6 benchmark", "2001:2::1"},
		{"ipv6 orchid", "2001:10::1"},
		{"ipv6 orchidv2", "2001:20::1"},

		// IPv4-mapped IPv6 bypasses (must unmap and match IPv4 denylist)
		{"ipv4-mapped loopback", "::ffff:127.0.0.1"},
		{"ipv4-mapped imds", "::ffff:169.254.169.254"},
		{"ipv4-mapped rfc1918 10", "::ffff:10.0.0.1"},
		{"ipv4-mapped rfc1918 192", "::ffff:192.168.1.1"},
		{"ipv4-mapped cgnat", "::ffff:100.64.0.1"},

		// IPv4-embedding transition/legacy ranges
		{"ipv4-compatible loopback", "::127.0.0.1"},
		{"ipv4-compatible imds", "::169.254.169.254"},
		{"ipv4-translated loopback", "::ffff:0:127.0.0.1"},
		{"ipv4-translated imds", "::ffff:0:169.254.169.254"},
		{"nat64 well-known imds", "64:ff9b::169.254.169.254"},
		{"nat64 local-use rfc1918", "64:ff9b:1::10.0.0.1"},
		{"6to4 loopback", "2002:7f00:0001::1"},
		{"6to4 imds", "2002:a9fe:a9fe::1"},
		{"teredo", "2001:0000:4136:e378:8000:63bf:3fff:fdd2"},

		// IPv6 zone IDs (always blocked, even for public IPv6!)
		{"ipv6 zone link-local", "fe80::1%eth0"},
		{"ipv6 zone loopback", "::1%lo0"},
		{"ipv6 zone public", "2606:2800:220:1:248:1893:25c8:1946%en0"},
	}

	for _, tc := range blocked {
		t.Run("blocked/"+tc.name, func(t *testing.T) {
			t.Parallel()
			ip := netip.MustParseAddr(tc.ip)
			err := checkIP(ip, nil, false)
			if err == nil {
				t.Fatalf("checkIP(%s) = nil, want blocked error", tc.ip)
			}
			var polErr *policyError
			if !errors.As(err, &polErr) || polErr.code != ErrCodeBlockedIP {
				t.Fatalf("checkIP(%s) err = %v, want policyError(%s)", tc.ip, err, ErrCodeBlockedIP)
			}
		})
	}

	allowed := []string{
		"1.1.1.1",
		"8.8.8.8",
		"93.184.216.34",
		"100.63.255.255",
		"100.128.0.1",
		"172.15.255.255",
		"172.32.0.1",
		"2606:2800:220:1:248:1893:25c8:1946",
		"::ffff:93.184.216.34", // unmaps to public IPv4
	}
	for _, raw := range allowed {
		t.Run("allowed/"+raw, func(t *testing.T) {
			t.Parallel()
			ip := netip.MustParseAddr(raw)
			if err := checkIP(ip, nil, false); err != nil {
				t.Fatalf("checkIP(%s) unexpected error: %v", raw, err)
			}
		})
	}

	t.Run("allowlist override and zone rejection", func(t *testing.T) {
		t.Parallel()
		allow := normalizeAllowCIDRs([]netip.Prefix{
			netip.MustParsePrefix("10.20.0.0/16"),
			netip.MustParsePrefix("127.0.0.1/32"),
			netip.MustParsePrefix("fd00::/8"),
		})

		if err := checkIP(netip.MustParseAddr("10.20.1.5"), allow, false); err != nil {
			t.Errorf("allowlisted 10.20.1.5 rejected: %v", err)
		}
		if err := checkIP(netip.MustParseAddr("::ffff:127.0.0.1"), allow, false); err != nil {
			t.Errorf("allowlisted ::ffff:127.0.0.1 rejected: %v", err)
		}
		if err := checkIP(netip.MustParseAddr("10.20.1.5"), allow, true); err != nil {
			t.Errorf("allowlisted local-hostname IP rejected: %v", err)
		}
		if err := checkIP(netip.MustParseAddr("93.184.216.34"), allow, true); err == nil {
			t.Errorf("local hostname resolving to non-allowlisted public IP should be rejected")
		}
		if err := checkIP(netip.MustParseAddr("127.0.0.2"), allow, false); err == nil {
			t.Errorf("non-allowlisted 127.0.0.2 should still be rejected")
		}
		if err := checkIP(netip.MustParseAddr("fd00::1%eth0"), allow, false); err == nil {
			t.Errorf("zoned IPv6 address must be rejected even when base IP is in ALLOW_CIDRS")
		}
	})
}

// FR-120, ADR 0010: URL pre-validation checks schemes, credentials, ports,
// trailing-dot local hostnames, IPv6 zones, and literal private IPs.
func TestValidateTargetURL(t *testing.T) {
	t.Parallel()

	defaultPorts := map[int]struct{}{80: {}, 443: {}}

	cases := []struct {
		name     string
		rawURL   string
		ports    map[int]struct{}
		cidrs    []netip.Prefix
		wantCode string
	}{
		{name: "https default port", rawURL: "https://example.com/path"},
		{name: "http default port", rawURL: "http://example.com/path"},
		{name: "explicit 443", rawURL: "https://example.com:443/path"},
		{name: "ftp scheme blocked", rawURL: "ftp://example.com/file", wantCode: ErrCodeBlockedScheme},
		{name: "file scheme blocked", rawURL: "file:///etc/passwd", wantCode: ErrCodeBlockedScheme},
		{name: "gopher scheme blocked", rawURL: "gopher://127.0.0.1:70/", wantCode: ErrCodeBlockedScheme},
		{name: "javascript scheme blocked", rawURL: "javascript:alert(1)", wantCode: ErrCodeBlockedScheme},
		//nolint:gosec // G101: synthetic test URL exercising credentials_in_url rejection
		{name: "credentials blocked", rawURL: "https://user:pass@example.com/", wantCode: ErrCodeCredentialsInURL},
		{name: "username only blocked", rawURL: "http://admin@example.com/", wantCode: ErrCodeCredentialsInURL},
		{name: "empty host blocked", rawURL: "https:///path", wantCode: ErrCodeInvalidURL},
		{name: "disallowed port 8080", rawURL: "http://example.com:8080/", wantCode: ErrCodeBlockedPort},
		{name: "disallowed port 22", rawURL: "http://example.com:22/", wantCode: ErrCodeBlockedPort},
		{name: "custom port allowed", rawURL: "http://example.com:8080/", ports: map[int]struct{}{80: {}, 443: {}, 8080: {}}},
		{name: "localhost blocked", rawURL: "http://localhost/test", wantCode: ErrCodeBlockedHost},
		{name: "localhost trailing dot blocked", rawURL: "http://localhost./test", wantCode: ErrCodeBlockedHost},
		{name: "localhost double trailing dot blocked", rawURL: "http://LOCALHOST../test", wantCode: ErrCodeBlockedHost},
		{name: "sub localhost blocked", rawURL: "http://app.localhost./test", wantCode: ErrCodeBlockedHost},
		{name: "dot local blocked", rawURL: "http://printer.local./test", wantCode: ErrCodeBlockedHost},
		{name: "dot internal blocked", rawURL: "http://metadata.google.internal./", wantCode: ErrCodeBlockedHost},
		{name: "dot internal deferred to DialContext when allowCIDRs set", rawURL: "http://staging.internal/", cidrs: []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")}},
		{name: "literal loopback blocked", rawURL: "http://127.0.0.1/", wantCode: ErrCodeBlockedIP},
		{name: "literal imds blocked", rawURL: "http://169.254.169.254/latest/meta-data/", wantCode: ErrCodeBlockedIP},
		{name: "literal ipv6 loopback blocked", rawURL: "http://[::1]/", wantCode: ErrCodeBlockedIP},
		{name: "literal ipv4-mapped imds blocked", rawURL: "http://[::ffff:169.254.169.254]/", wantCode: ErrCodeBlockedIP},
		{name: "literal ipv6 zone blocked", rawURL: "http://[fe80::1%25eth0]/", wantCode: ErrCodeBlockedIP},
		{name: "literal allowlisted loopback allowed", rawURL: "http://127.0.0.1/", cidrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ports := tc.ports
			if ports == nil {
				ports = defaultPorts
			}
			u, err := url.Parse(tc.rawURL)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", tc.rawURL, err)
			}
			err = validateTargetURL(u, ports, normalizeAllowCIDRs(tc.cidrs))
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("validateTargetURL(%q) unexpected error: %v", tc.rawURL, err)
				}
				return
			}
			var polErr *policyError
			if !errors.As(err, &polErr) || polErr.code != tc.wantCode {
				t.Fatalf("validateTargetURL(%q) err = %v, want code %q", tc.rawURL, err, tc.wantCode)
			}
		})
	}
}
