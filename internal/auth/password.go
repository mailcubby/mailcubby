package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters mandated by SPEC FR-042 (OWASP minimum).
const (
	Argon2Memory        uint32 = 19456 // KiB (19 MiB)
	Argon2Time          uint32 = 2
	Argon2Threads       uint8  = 1
	Argon2KeyLen        uint32 = 32
	Argon2SaltLen              = 16
	MinPasswordLen             = 10
	MaxPasswordLen             = 256
	MaxPasswordBytes           = 1024
	maxConcurrentHashes        = 2
)

// dummyPHCHash is a syntactically valid argon2id PHC string using the exact
// production parameters (16-byte salt, 32-byte digest). It is used to run an
// identical argon2id verification when a login targets an unknown email,
// disabled account, or passwordless user (SPEC FR-043).
const dummyPHCHash = "$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

var (
	// ErrInvalidPasswordHash indicates a malformed or unsupported PHC hash string.
	ErrInvalidPasswordHash = errors.New("auth: invalid password hash format")
	// ErrPasswordLength indicates a password outside the 10..256 character range (SPEC FR-042).
	ErrPasswordLength = errors.New("auth: password must be between 10 and 256 characters")
)

// ValidatePassword checks that password satisfies SPEC FR-042 length constraints.
func ValidatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	if n < MinPasswordLen || n > MaxPasswordLen || len(password) > MaxPasswordBytes {
		return ErrPasswordLength
	}
	return nil
}

// Hasher performs argon2id password hashing and verification with bounded
// concurrency (at most 2 concurrent operations per SPEC FR-042).
type Hasher struct {
	sem chan struct{}
}

// NewHasher returns a new argon2id Hasher enforcing the concurrency semaphore.
func NewHasher() *Hasher {
	return &Hasher{
		sem: make(chan struct{}, maxConcurrentHashes),
	}
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hasher) release() {
	<-h.sem
}

// Hash hashes password with argon2id and returns a PHC-encoded string starting
// with "$argon2id$v=19$m=19456,t=2,p=1$" (SPEC FR-042).
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, Argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate password salt: %w", err)
	}

	if err := h.acquire(ctx); err != nil {
		return "", fmt.Errorf("auth: wait for hash slot: %w", err)
	}
	defer h.release()

	key := argon2.IDKey([]byte(password), salt, Argon2Time, Argon2Memory, Argon2Threads, Argon2KeyLen)
	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		Argon2Memory,
		Argon2Time,
		Argon2Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return encoded, nil
}

// Verify checks password against an argon2id PHC-encoded hash.
// It returns matched=true on success, and needsRehash=true if the stored hash
// uses different argon2id parameters than the current defaults (SPEC FR-042).
func (h *Hasher) Verify(ctx context.Context, password, encodedHash string) (matched bool, needsRehash bool, err error) {
	params, salt, expected, err := parsePHC(encodedHash)
	if err != nil {
		return false, false, err
	}

	if err := h.acquire(ctx); err != nil {
		return false, false, fmt.Errorf("auth: wait for verify slot: %w", err)
	}
	defer h.release()

	// Cap derived key length to expected length (already bounded in parsePHC).
	//nolint:gosec // G115: len(expected) is validated to be in [16, 128] in parsePHC.
	keyLen := uint32(len(expected))
	derived := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, keyLen)
	if subtle.ConstantTimeCompare(derived, expected) != 1 {
		return false, false, nil
	}

	rehash := params.memory != Argon2Memory ||
		params.time != Argon2Time ||
		params.threads != Argon2Threads ||
		keyLen != Argon2KeyLen
	return true, rehash, nil
}

// VerifyDummy runs a full argon2id verification against a dummy hash with the
// current parameters so failed logins for unknown, disabled, or passwordless
// accounts perform identical work (SPEC FR-043).
func (h *Hasher) VerifyDummy(ctx context.Context, password string) error {
	_, _, err := h.Verify(ctx, password, dummyPHCHash)
	return err
}

type phcParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func parsePHC(encoded string) (phcParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return phcParams{}, nil, nil, ErrInvalidPasswordHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return phcParams{}, nil, nil, ErrInvalidPasswordHash
	}

	var p phcParams
	var seenM, seenT, seenP bool
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return phcParams{}, nil, nil, ErrInvalidPasswordHash
		}
		switch k {
		case "m":
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil || n == 0 {
				return phcParams{}, nil, nil, ErrInvalidPasswordHash
			}
			p.memory = uint32(n)
			seenM = true
		case "t":
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil || n == 0 {
				return phcParams{}, nil, nil, ErrInvalidPasswordHash
			}
			p.time = uint32(n)
			seenT = true
		case "p":
			n, err := strconv.ParseUint(v, 10, 8)
			if err != nil || n == 0 {
				return phcParams{}, nil, nil, ErrInvalidPasswordHash
			}
			p.threads = uint8(n)
			seenP = true
		default:
			return phcParams{}, nil, nil, ErrInvalidPasswordHash
		}
	}
	if !seenM || !seenT || !seenP {
		return phcParams{}, nil, nil, ErrInvalidPasswordHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return phcParams{}, nil, nil, ErrInvalidPasswordHash
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(hash) < 16 || len(hash) > 128 {
		return phcParams{}, nil, nil, ErrInvalidPasswordHash
	}

	return p, salt, hash, nil
}
