package secrets

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseAppKey(t *testing.T) {
	t.Parallel()

	raw := make([]byte, KeySize)
	for i := range raw {
		raw[i] = byte(i*7 + 3)
	}

	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{name: "std", in: base64.StdEncoding.EncodeToString(raw)},
		{name: "raw std", in: base64.RawStdEncoding.EncodeToString(raw)},
		{name: "url", in: base64.URLEncoding.EncodeToString(raw)},
		{name: "raw url", in: base64.RawURLEncoding.EncodeToString(raw)},
		{name: "with whitespace", in: "  " + base64.StdEncoding.EncodeToString(raw) + "\n"},
		{name: "empty", in: "", wantErr: true},
		{name: "too short", in: base64.StdEncoding.EncodeToString(raw[:16]), wantErr: true},
		{name: "too long", in: base64.StdEncoding.EncodeToString(append(append([]byte(nil), raw...), 1)), wantErr: true},
		{name: "invalid base64", in: "not!valid!base64!!!", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseAppKey(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseAppKey(%q) succeeded, want error", tc.in)
				}
				if !strings.Contains(err.Error(), "mailcubby keygen") {
					t.Fatalf("ParseAppKey(%q) error = %q, want hint mentioning mailcubby keygen", tc.in, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAppKey(%q) unexpected error: %v", tc.in, err)
			}
			if !bytes.Equal(got, raw) {
				t.Fatalf("ParseAppKey(%q) decoded bytes mismatch", tc.in)
			}
		})
	}
}

func TestGenerateAppKey(t *testing.T) {
	t.Parallel()

	k1, s1, err := GenerateAppKey()
	if err != nil {
		t.Fatalf("GenerateAppKey: %v", err)
	}
	k2, s2, err := GenerateAppKey()
	if err != nil {
		t.Fatalf("GenerateAppKey second: %v", err)
	}
	if len(k1) != KeySize {
		t.Fatalf("len(k1) = %d, want %d", len(k1), KeySize)
	}
	if bytes.Equal(k1, k2) || s1 == s2 {
		t.Fatalf("two generated keys must not be equal")
	}
	parsed, err := ParseAppKey(s1)
	if err != nil {
		t.Fatalf("ParseAppKey(generated): %v", err)
	}
	if !bytes.Equal(parsed, k1) {
		t.Fatalf("parsed key does not match generated raw bytes")
	}
}

func TestLoadOrCreateLocalKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	k1, err := LoadOrCreateLocalKey(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateLocalKey first: %v", err)
	}
	if len(k1) != KeySize {
		t.Fatalf("len(k1) = %d, want %d", len(k1), KeySize)
	}

	keyPath := filepath.Join(dir, "app.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat app.key: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("app.key mode = %o, want 0600", info.Mode().Perm())
	}

	k2, err := LoadOrCreateLocalKey(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateLocalKey second: %v", err)
	}
	if !bytes.Equal(k1, k2) {
		t.Fatalf("second LoadOrCreateLocalKey returned a different key")
	}

	// Enforce 0600 on an existing key file with overly permissive mode.
	if runtime.GOOS != "windows" {
		//nolint:gosec // G302: intentionally loosening mode in test to verify LoadOrCreateLocalKey tightens it back to 0600
		if err := os.Chmod(keyPath, 0o644); err != nil {
			t.Fatalf("chmod 0644: %v", err)
		}
		k3, err := LoadOrCreateLocalKey(dir)
		if err != nil {
			t.Fatalf("LoadOrCreateLocalKey chmod tighten: %v", err)
		}
		if !bytes.Equal(k1, k3) {
			t.Fatalf("key changed when tightening permissions")
		}
		info, err = os.Stat(keyPath)
		if err != nil {
			t.Fatalf("stat after tighten: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("app.key mode after tighten = %o, want 0600", info.Mode().Perm())
		}
	}

	// Empty 0-byte key file (e.g. interrupted startup) is recovered cleanly.
	emptyDir := t.TempDir()
	emptyPath := filepath.Join(emptyDir, "app.key")
	//nolint:gosec // G306: intentionally creating loose empty file to test recovery and mode tightening
	if err := os.WriteFile(emptyPath, nil, 0o644); err != nil {
		t.Fatalf("write empty app.key: %v", err)
	}
	recovered, err := LoadOrCreateLocalKey(emptyDir)
	if err != nil {
		t.Fatalf("LoadOrCreateLocalKey empty file recovery: %v", err)
	}
	if len(recovered) != KeySize {
		t.Fatalf("recovered key len = %d, want %d", len(recovered), KeySize)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(emptyPath)
		if err != nil {
			t.Fatalf("stat recovered app.key: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("recovered app.key mode = %o, want 0600", info.Mode().Perm())
		}
	}
}

func TestSealOpenRoundTripAndFailures(t *testing.T) {
	t.Parallel()

	// FR-061
	key1, _, err := GenerateAppKey()
	if err != nil {
		t.Fatalf("GenerateAppKey 1: %v", err)
	}
	key2, _, err := GenerateAppKey()
	if err != nil {
		t.Fatalf("GenerateAppKey 2: %v", err)
	}

	c1, err := NewSMTPCredentialCipher(key1)
	if err != nil {
		t.Fatalf("NewSMTPCredentialCipher 1: %v", err)
	}
	c2, err := NewSMTPCredentialCipher(key2)
	if err != nil {
		t.Fatalf("NewSMTPCredentialCipher 2: %v", err)
	}

	plaintext := []byte("AbCdEfGh12345678IjKlMnOp")
	aad := []byte("shop-a1b2c3d4")

	sealed1, err := c1.Seal(plaintext, aad)
	if err != nil {
		t.Fatalf("Seal 1: %v", err)
	}
	sealed2, err := c1.Seal(plaintext, aad)
	if err != nil {
		t.Fatalf("Seal 2: %v", err)
	}
	if sealed1[0] != VersionByte {
		t.Fatalf("version byte = 0x%02x, want 0x%02x", sealed1[0], VersionByte)
	}
	if bytes.Equal(sealed1, sealed2) {
		t.Fatalf("two Seal calls produced identical ciphertexts (nonce reuse)")
	}
	if bytes.Contains(sealed1, plaintext) {
		t.Fatalf("sealed output contains plaintext")
	}

	got, err := c1.Open(sealed1, aad)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Open = %q, want %q", got, plaintext)
	}

	if _, err := c2.Open(sealed1, aad); err == nil {
		t.Fatalf("Open with different key succeeded, want error")
	}
	if _, err := c1.Open(sealed1, []byte("other-user")); err == nil {
		t.Fatalf("Open with different AAD succeeded, want error")
	}

	tampered := append([]byte(nil), sealed1...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := c1.Open(tampered, aad); err == nil {
		t.Fatalf("Open with tampered ciphertext succeeded, want error")
	}

	badVersion := append([]byte(nil), sealed1...)
	badVersion[0] = 0x02
	if _, err := c1.Open(badVersion, aad); err == nil {
		t.Fatalf("Open with wrong version byte succeeded, want error")
	}

	if _, err := c1.Open(sealed1[:5], aad); err == nil {
		t.Fatalf("Open with truncated ciphertext succeeded, want error")
	}
}

func TestDeriveSubkeyDomainSeparation(t *testing.T) {
	t.Parallel()

	key, _, err := GenerateAppKey()
	if err != nil {
		t.Fatalf("GenerateAppKey: %v", err)
	}
	s1, err := DeriveSubkey(key, InfoSMTPCredential)
	if err != nil {
		t.Fatalf("DeriveSubkey 1: %v", err)
	}
	s2, err := DeriveSubkey(key, "mailcubby/other/v1")
	if err != nil {
		t.Fatalf("DeriveSubkey 2: %v", err)
	}
	if bytes.Equal(s1, s2) {
		t.Fatalf("distinct HKDF info strings produced identical subkeys")
	}
}
