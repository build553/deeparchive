package identity

import "deeparchive/internal/tokens"

// generateOpaqueToken and hashToken delegate to the shared internal/tokens
// package (used by every domain needing bearer-style single-use or
// revocable credentials) so this package doesn't define its own slightly
// different entropy/hashing decisions.
func generateOpaqueToken() (string, error) { return tokens.Generate() }

func hashToken(token string) string { return tokens.Hash(token) }
