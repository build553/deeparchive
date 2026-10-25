package identity

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// AccessClaims is the payload of a short-lived access token. It is
// intentionally minimal: identity + session linkage only. Authorization
// decisions (org role, platform role) are re-derived from the database on
// each request rather than trusted from the token, so a role change or
// membership removal takes effect immediately rather than only after token
// expiry.
type AccessClaims struct {
	jwt.RegisteredClaims
	UserID    uuid.UUID `json:"uid"`
	SessionID uuid.UUID `json:"sid"`
}

type AccessTokenIssuer struct {
	signingKey []byte
	ttl        time.Duration
}

func NewAccessTokenIssuer(signingKey []byte, ttl time.Duration) *AccessTokenIssuer {
	return &AccessTokenIssuer{signingKey: signingKey, ttl: ttl}
}

func (i *AccessTokenIssuer) Issue(userID, sessionID uuid.UUID) (string, time.Time, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(i.ttl)
	claims := AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			Subject:   userID.String(),
		},
		UserID:    userID,
		SessionID: sessionID,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(i.signingKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signing access token: %w", err)
	}
	return signed, expiresAt, nil
}

// Verify parses and validates a signed access token, rejecting anything
// signed with an unexpected algorithm (defense against algorithm-confusion
// attacks) or expired per standard JWT exp validation (handled by the
// library).
func (i *AccessTokenIssuer) Verify(tokenString string) (*AccessClaims, error) {
	claims := &AccessClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return i.signingKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCredentials, err)
	}
	return claims, nil
}
