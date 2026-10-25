// Package tokens generates and hashes opaque bearer tokens shared by any
// domain that needs single-use or revocable credentials (session refresh
// tokens, email verification, password reset, organization invitations).
// Centralized here so every caller gets the same entropy and hashing
// decisions instead of each domain reinventing them slightly differently.
package tokens

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// Generate returns a URL-safe random token with 256 bits of entropy,
// suitable for emailing to a user or handing to a client as a bearer
// credential. Only its Hash is ever stored.
func Generate() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Hash deterministically hashes an opaque bearer token for storage and
// lookup. SHA-256 (not a slow KDF) is appropriate because the input
// already has 256 bits of entropy — unlike a user-chosen password, it is
// not guessable by brute force, so a slow KDF would only add unnecessary
// CPU cost to every authenticated request.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
