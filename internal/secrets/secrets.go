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
		return nil, errors.New("secrets: empty app key (generate one with: mailcubby keygen)")
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
				return nil, fmt.Errorf("secrets: app key must be %d bytes, got %d (generate one with: mailcubby keygen)", KeySize, len(b))
			}
			return b, nil
		}
	}
	return nil, errors.New("secrets: app key must be valid base64 (standard or URL) encoding of 32 bytes (generate one with: mailcubby keygen)")
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
// creating it atomically with mode 0600 if it does not yet exist or is empty
// (e.g. after a crash before writing). Existing key files have 0600 permissions
// enforced (SPEC 5.1).
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
		if strings.TrimSpace(string(data)) != "" {
			key, parseErr := ParseAppKey(string(data))
			if parseErr != nil {
				return nil, fmt.Errorf("secrets: parse %s: %w", path, parseErr)
			}
			if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
				return nil, fmt.Errorf("secrets: chmod %s: %w", path, chmodErr)
			}
			return key, nil
		}
		// Empty/partial key file left behind by an interrupted startup: remove and regenerate.
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return nil, fmt.Errorf("secrets: remove empty %s: %w", path, rmErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("secrets: read %s: %w", path, err)
	}

	key, encoded, err := GenerateAppKey()
	if err != nil {
		return nil, err
	}

	tmp, err := os.CreateTemp(dataDir, ".app.key.tmp-*")
	if err != nil {
		return nil, fmt.Errorf("secrets: create temp key file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanupTmp := func() {
		_ = os.Remove(tmpPath)
	}

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanupTmp()
		return nil, fmt.Errorf("secrets: chmod temp key file: %w", err)
	}
	if _, err := tmp.WriteString(encoded + "\n"); err != nil {
		_ = tmp.Close()
		cleanupTmp()
		return nil, fmt.Errorf("secrets: write temp key file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanupTmp()
		return nil, fmt.Errorf("secrets: sync temp key file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanupTmp()
		return nil, fmt.Errorf("secrets: close temp key file: %w", err)
	}

	// Atomically link into place so two concurrent startups never overwrite each other.
	if err := os.Link(tmpPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			cleanupTmp()
			//nolint:gosec // G304: localKeyFilename is joined with configured dataDir
			existing, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, fmt.Errorf("secrets: read %s after race: %w", path, readErr)
			}
			parsed, parseErr := ParseAppKey(string(existing))
			if parseErr != nil {
				return nil, fmt.Errorf("secrets: parse %s after race: %w", path, parseErr)
			}
			_ = os.Chmod(path, 0o600)
			return parsed, nil
		}
		// Fallback to Rename on filesystems that do not support hard links.
		if renErr := os.Rename(tmpPath, path); renErr != nil {
			cleanupTmp()
			return nil, fmt.Errorf("secrets: place %s: %w", path, renErr)
		}
		return key, nil
	}
	cleanupTmp()

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
