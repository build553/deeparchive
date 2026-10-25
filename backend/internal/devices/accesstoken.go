package devices

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// DeviceClaims is the payload of a device-scoped access token, issued
// after a device completes challenge/response authentication (see
// Service.Authenticate). It is distinct from identity.AccessClaims:
// a device token represents "this device, owned by this user" and is
// what the Rust backup engine will present to upload/job endpoints in
// later milestones — a user's browser session token must never be
// accepted in that role, and vice versa.
type DeviceClaims struct {
	jwt.RegisteredClaims
	DeviceID uuid.UUID `json:"did"`
	UserID   uuid.UUID `json:"uid"`
}

type AccessTokenIssuer struct {
	signingKey []byte
	ttl        time.Duration
}

func NewAccessTokenIssuer(signingKey []byte, ttl time.Duration) *AccessTokenIssuer {
	return &AccessTokenIssuer{signingKey: signingKey, ttl: ttl}
}

func (i *AccessTokenIssuer) Issue(deviceID, userID uuid.UUID) (string, time.Time, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(i.ttl)
	claims := DeviceClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			Subject:   deviceID.String(),
		},
		DeviceID: deviceID,
		UserID:   userID,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(i.signingKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signing device access token: %w", err)
	}
	return signed, expiresAt, nil
}

func (i *AccessTokenIssuer) Verify(tokenString string) (*DeviceClaims, error) {
	claims := &DeviceClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return i.signingKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid device token: %w", err)
	}
	return claims, nil
}
