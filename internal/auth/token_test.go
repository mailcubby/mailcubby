package auth

import (
	"strings"
	"testing"
	"time"
)

// FR-065: token format, prefix extraction, and constant-time hash verification.
func TestGenerateAndParseAPIToken(t *testing.T) {
	t.Parallel()

	raw, prefix, hash, err := GenerateAPIToken()
	if err != nil {
		t.Fatalf("GenerateAPIToken: %v", err)
	}
	if !strings.HasPrefix(raw, APITokenPrefix) {
		t.Fatalf("raw token %q missing prefix %q", raw, APITokenPrefix)
	}
	if len(raw) != len(APITokenPrefix)+APITokenBodyLen {
		t.Fatalf("len(raw) = %d, want %d", len(raw), len(APITokenPrefix)+APITokenBodyLen)
	}
	if len(prefix) != APITokenPrefixLen {
		t.Fatalf("len(prefix) = %d, want %d", len(prefix), APITokenPrefixLen)
	}
	if gotPrefix, ok := ParseAPIToken(raw); !ok || gotPrefix != prefix {
		t.Fatalf("ParseAPIToken(%q) = (%q, %v), want (%q, true)", raw, gotPrefix, ok, prefix)
	}
	if !VerifyAPITokenHash(raw, hash) {
		t.Fatal("VerifyAPITokenHash failed for matching token")
	}
	if VerifyAPITokenHash(raw+"x", hash) {
		t.Fatal("VerifyAPITokenHash succeeded for modified token")
	}
	if VerifyAPITokenHash(raw, "") {
		t.Fatal("VerifyAPITokenHash succeeded for empty expected hash")
	}
}

func TestParseAPITokenRejectsInvalid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"wrong prefix", "mc_" + strings.Repeat("a", 40)},
		{"too short", "mailcubby_" + strings.Repeat("a", 39)},
		{"too long", "mailcubby_" + strings.Repeat("a", 41)},
		{"non-base62 dash", "mailcubby_" + strings.Repeat("a", 39) + "-"},
		{"non-base62 space", "mailcubby_" + strings.Repeat("a", 39) + " "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, ok := ParseAPIToken(tc.raw); ok {
				t.Fatalf("ParseAPIToken(%q) = ok, want false", tc.raw)
			}
		})
	}
}

func TestValidAPITokenScopeAndTouch(t *testing.T) {
	t.Parallel()

	if !ValidAPITokenScope("read") || !ValidAPITokenScope("write") {
		t.Fatal("expected read and write to be valid scopes")
	}
	if ValidAPITokenScope("owner") || ValidAPITokenScope("") || ValidAPITokenScope("READ") {
		t.Fatal("expected invalid scopes to be rejected")
	}

	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	if !NeedsAPITokenTouch(now, nil) {
		t.Fatal("expected nil last_used_at to trigger touch")
	}
	recent := now.Add(-30 * time.Second)
	if NeedsAPITokenTouch(now, &recent) {
		t.Fatal("expected touch within 1 minute to be throttled")
	}
	stale := now.Add(-time.Minute)
	if !NeedsAPITokenTouch(now, &stale) {
		t.Fatal("expected touch at >= 1 minute to trigger update")
	}
}
