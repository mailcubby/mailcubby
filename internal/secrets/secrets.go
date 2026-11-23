// Package secrets handles MAILCUBBY_APP_KEY loading and generation, HKDF
// subkey derivation, and AES-256-GCM authenticated encryption (SPEC FR-061).
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// KeySize is the required byte length of MAILCUBBY_APP_KEY and derived subkeys.
	KeySize = 32
	// VersionByte identifies the v1 sealed ciphertext format:
	// 0x01 || 12-byte nonce || AES-256-GCM ciphertext+tag.
	VersionByte byte = 0x01
	// InfoSMTPCredential is the HKDF info string for SMTP credential encryption (SPEC FR-061).
	//nolint:gosec // G101: public HKDF domain-separation label, not a secret
	InfoSMTPCredential = "mailcubby/smtp-credential/v1"

	localKeyFilename = "app.key"
)

// ParseAppKey decodes a base64 (standard or URL, padded or unpadded) 32-byte key.
func ParseAppKey(encoded string) ([]byte, error) {
	s := strings.TrimSpace(encoded)
	if s == "" {
		return nil, errors.New("secrets: empty app key")
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		b, err := enc.DecodeString(s)
		if err == nil {
			if len(b) != KeySize {
				return nil, fmt.Errorf("secrets: app key must be %d bytes, got %d", KeySize, len(b))
			}
			return b, nil
		}
	}
	return nil, errors.New("secrets: app key must be valid base64 (standard or URL) encoding of 32 bytes")
}

// GenerateAppKey generates a cryptographically random 32-byte key and returns
// both the raw bytes and standard base64 encoding.
func GenerateAppKey() ([]byte, string, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, "", fmt.Errorf("secrets: generate app key: %w", err)
	}
	return key, base64.StdEncoding.EncodeToString(key), nil
}

// LoadOrCreateLocalKey loads the local-mode app key from <dataDir>/app.key,
// creating it with mode 0600 if it does not yet exist (SPEC 5.1).
func LoadOrCreateLocalKey(dataDir string) ([]byte, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("secrets: empty data dir")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("secrets: create data dir: %w", err)
	}

	path := filepath.Join(dataDir, localKeyFilename)
	//nolint:gosec // G304: localKeyFilename is joined with configured dataDir
	data, err := os.ReadFile(path)
	if err == nil {
		key, parseErr := ParseAppKey(string(data))
		if parseErr != nil {
			return nil, fmt.Errorf("secrets: parse %s: %w", path, parseErr)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("secrets: read %s: %w", path, err)
	}

	key, encoded, err := GenerateAppKey()
	if err != nil {
		return nil, err
	}

	//nolint:gosec // G304: localKeyFilename is joined with configured dataDir
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// Another startup created the file concurrently; read it.
			//nolint:gosec // G304: localKeyFilename is joined with configured dataDir
			existing, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, fmt.Errorf("secrets: read %s after race: %w", path, readErr)
			}
			return ParseAppKey(string(existing))
		}
		return nil, fmt.Errorf("secrets: create %s: %w", path, err)
	}

	if _, err := f.WriteString(encoded + "\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("secrets: write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("secrets: sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("secrets: close %s: %w", path, err)
	}

	return key, nil
}

// DeriveSubkey derives a 32-byte subkey from appKey using HKDF-SHA256 with the
// given domain-separation info string.
func DeriveSubkey(appKey []byte, info string) ([]byte, error) {
	if len(appKey) != KeySize {
		return nil, fmt.Errorf("secrets: app key must be %d bytes, got %d", KeySize, len(appKey))
	}
	if info == "" {
		return nil, errors.New("secrets: hkdf info must not be empty")
	}
	subkey, err := hkdf.Key(sha256.New, appKey, nil, info, KeySize)
	if err != nil {
		return nil, fmt.Errorf("secrets: derive subkey: %w", err)
	}
	return subkey, nil
}

// Cipher performs versioned AES-256-GCM authenticated encryption with associated data.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher constructs a Cipher from a 32-byte key.
func NewCipher(subkey []byte) (*Cipher, error) {
	if len(subkey) != KeySize {
		return nil, fmt.Errorf("secrets: subkey must be %d bytes, got %d", KeySize, len(subkey))
	}
	block, err := aes.NewCipher(subkey)
	if err != nil {
		return nil, fmt.Errorf("secrets: init aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: init gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// NewSMTPCredentialCipher derives the SMTP credential subkey from appKey and
// returns a Cipher ready to seal and open SMTP passwords (SPEC FR-061).
func NewSMTPCredentialCipher(appKey []byte) (*Cipher, error) {
	subkey, err := DeriveSubkey(appKey, InfoSMTPCredential)
	if err != nil {
		return nil, err
	}
	return NewCipher(subkey)
}

// Seal encrypts plaintext with optional associated data aad and returns
// VersionByte || 12-byte random nonce || ciphertext+tag.
func (c *Cipher) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secrets: generate nonce: %w", err)
	}
	out := make([]byte, 1+len(nonce), 1+len(nonce)+len(plaintext)+c.aead.Overhead())
	out[0] = VersionByte
	copy(out[1:], nonce)
	return c.aead.Seal(out, nonce, plaintext, aad), nil
}

// Open verifies and decrypts sealed data produced by Seal with the matching aad.
func (c *Cipher) Open(sealed, aad []byte) ([]byte, error) {
	nonceSize := c.aead.NonceSize()
	if len(sealed) < 1+nonceSize+c.aead.Overhead() {
		return nil, errors.New("secrets: ciphertext too short")
	}
	if sealed[0] != VersionByte {
		return nil, fmt.Errorf("secrets: unsupported version byte 0x%02x", sealed[0])
	}
	nonce := sealed[1 : 1+nonceSize]
	ciphertext := sealed[1+nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("secrets: open ciphertext: %w", err)
	}
	return plaintext, nil
}
