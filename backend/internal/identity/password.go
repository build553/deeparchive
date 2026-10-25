package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. These are process-wide constants rather than
// per-call configuration: changing them requires a deliberate migration
// (existing hashes remain verifiable since the parameters are encoded in
// the stored hash string itself; see encodeHash/decodeHash).
const (
	argonTime     = 3
	argonMemoryKB = 64 * 1024 // 64 MiB
	argonThreads  = 4
	argonKeyLen   = 32
	saltLen       = 16

	// MinPasswordLength enforces a minimum length only — composition rules
	// (requiring symbols, digits, etc.) are deliberately not enforced; they
	// push users toward predictable patterns and are discouraged by current
	// NIST guidance in favor of length and breach-list checks.
	MinPasswordLength = 12
	MaxPasswordLength = 256
)

var ErrPasswordPolicy = errors.New("password does not meet policy requirements")

// ValidatePasswordPolicy enforces length bounds. It intentionally does not
// reject passwords for missing character classes.
func ValidatePasswordPolicy(password string) error {
	n := utf8.RuneCountInString(password)
	if n < MinPasswordLength {
		return fmt.Errorf("%w: must be at least %d characters", ErrPasswordPolicy, MinPasswordLength)
	}
	if n > MaxPasswordLength {
		return fmt.Errorf("%w: must be at most %d characters", ErrPasswordPolicy, MaxPasswordLength)
	}
	return nil
}

// HashPassword hashes password with Argon2id using a freshly generated
// random salt, and encodes the result (algorithm + parameters + salt +
// hash) into a single self-describing string suitable for storage in
// users.password_hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKB, argonThreads, argonKeyLen)
	return encodeHash(salt, hash), nil
}

// VerifyPassword reports whether password matches the stored encoded hash,
// using constant-time comparison to avoid timing side channels.
func VerifyPassword(encoded, password string) (bool, error) {
	params, salt, hash, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	computed := argon2.IDKey([]byte(password), salt, params.time, params.memoryKB, params.threads, uint32(len(hash)))
	return subtle.ConstantTimeCompare(hash, computed) == 1, nil
}

type argonParams struct {
	time     uint32
	memoryKB uint32
	threads  uint8
}

// encodeHash follows the conventional Argon2 encoding so parameters travel
// with the hash and can be rotated without invalidating existing hashes:
// $argon2id$v=19$m=<kb>,t=<time>,p=<threads>$<salt>$<hash>
func encodeHash(salt, hash []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
}

func decodeHash(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, errors.New("unrecognized password hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argonParams{}, nil, nil, fmt.Errorf("parsing version: %w", err)
	}
	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memoryKB, &p.time, &p.threads); err != nil {
		return argonParams{}, nil, nil, fmt.Errorf("parsing params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, fmt.Errorf("decoding salt: %w", err)
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argonParams{}, nil, nil, fmt.Errorf("decoding hash: %w", err)
	}
	return p, salt, hash, nil
}
