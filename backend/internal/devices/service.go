// Package devices implements device registration and public-key
// challenge/response authentication.
//
// A device is only actually authenticated once it has signed a
// server-issued challenge and the signature has been verified against its
// registered Ed25519 public key (crypto/ed25519, stdlib — no custom
// signature scheme). Merely storing a public key at enrollment time is
// not authentication by itself; see Authenticate.
package devices

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/db/generated"
	"deeparchive/internal/audit"
	"deeparchive/internal/dbtypes"
)

var (
	ErrNotFound         = errors.New("device not found")
	ErrForbidden        = errors.New("forbidden")
	ErrRevoked          = errors.New("device has been revoked")
	ErrInvalidPublicKey = errors.New("public key must be a 32-byte Ed25519 key")
	ErrInvalidChallenge = errors.New("invalid or expired challenge")
	ErrInvalidSignature = errors.New("signature verification failed")
)

const challengeTTL = 2 * time.Minute

type Service struct {
	pool    *pgxpool.Pool
	queries *generated.Queries
	audit   *audit.Recorder
}

func NewService(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	queries := generated.New(pool)
	return &Service{pool: pool, queries: queries, audit: audit.NewRecorder(queries, logger)}
}

type Device struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	OrganizationID *uuid.UUID `json:"organization_id,omitempty"`
	Name           string     `json:"name"`
	OS             string     `json:"os"`
	ClientVersion  string     `json:"client_version"`
	Status         string     `json:"status"`
	RegisteredAt   time.Time  `json:"registered_at"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
}

func toDevice(d generated.Device) Device {
	return Device{
		ID:             dbtypes.FromUUID(d.ID),
		UserID:         dbtypes.FromUUID(d.UserID),
		OrganizationID: dbtypes.FromUUIDPtr(d.OrganizationID),
		Name:           d.Name,
		OS:             d.Os,
		ClientVersion:  d.ClientVersion,
		Status:         d.Status,
		RegisteredAt:   dbtypes.FromTimestamptz(d.RegisteredAt),
		LastSeenAt:     dbtypes.FromTimestamptzPtr(d.LastSeenAt),
	}
}

type EnrollParams struct {
	UserID         uuid.UUID
	OrganizationID *uuid.UUID
	Name           string
	OS             string
	ClientVersion  string
	PublicKey      []byte // raw Ed25519 public key, 32 bytes
}

// Enroll registers a new device in "pending" status. The caller (an
// authenticated user) must separately call Approve — in practice a
// self-service client does this immediately after enrolling, but
// separating the steps leaves room for an organization to later require
// admin approval of new devices without changing the enrollment API.
func (s *Service) Enroll(ctx context.Context, p EnrollParams) (*Device, error) {
	if len(p.PublicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidPublicKey
	}
	if p.Name == "" {
		return nil, fmt.Errorf("device name is required")
	}

	d, err := s.queries.CreateDevice(ctx, generated.CreateDeviceParams{
		UserID:         dbtypes.UUID(p.UserID),
		OrganizationID: dbtypes.UUIDPtr(p.OrganizationID),
		Name:           p.Name,
		Os:             p.OS,
		ClientVersion:  p.ClientVersion,
		PublicKey:      p.PublicKey,
	})
	if err != nil {
		return nil, fmt.Errorf("creating device: %w", err)
	}

	deviceID := dbtypes.FromUUID(d.ID)
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &p.UserID, EventType: "device.enrolled", TargetType: "device", TargetID: &deviceID,
	})

	result := toDevice(d)
	return &result, nil
}

// authorize returns the device if visible to userID (owner, or member of
// the device's organization), or ErrNotFound otherwise.
func (s *Service) authorize(ctx context.Context, userID, deviceID uuid.UUID) (generated.Device, error) {
	d, err := s.queries.GetDeviceByID(ctx, dbtypes.UUID(deviceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return generated.Device{}, ErrNotFound
		}
		return generated.Device{}, fmt.Errorf("looking up device: %w", err)
	}
	if dbtypes.FromUUID(d.UserID) == userID {
		return d, nil
	}
	if d.OrganizationID.Valid {
		if _, err := s.queries.GetMembership(ctx, generated.GetMembershipParams{
			OrganizationID: d.OrganizationID, UserID: dbtypes.UUID(userID),
		}); err == nil {
			return d, nil
		}
	}
	return generated.Device{}, ErrNotFound
}

func (s *Service) Get(ctx context.Context, userID, deviceID uuid.UUID) (*Device, error) {
	d, err := s.authorize(ctx, userID, deviceID)
	if err != nil {
		return nil, err
	}
	result := toDevice(d)
	return &result, nil
}

func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID) ([]Device, error) {
	rows, err := s.queries.ListDevicesForUser(ctx, dbtypes.UUID(userID))
	if err != nil {
		return nil, fmt.Errorf("listing devices: %w", err)
	}
	out := make([]Device, 0, len(rows))
	for _, r := range rows {
		out = append(out, toDevice(r))
	}
	return out, nil
}

// Approve requires the caller to be the device's own owner (personal
// devices have no admin concept) OR, for an organization-scoped device,
// an owner/admin of that organization specifically — a plain member
// (even the device's own enrolling user, if they only hold the member
// role) cannot approve a device into the org's shared storage scope on
// their own say-so. This was a known gap: previously any member could
// approve any pending device in their organization.
func (s *Service) Approve(ctx context.Context, userID, deviceID uuid.UUID) error {
	d, err := s.authorize(ctx, userID, deviceID)
	if err != nil {
		return err
	}
	if d.OrganizationID.Valid {
		m, err := s.queries.GetMembership(ctx, generated.GetMembershipParams{
			OrganizationID: d.OrganizationID, UserID: dbtypes.UUID(userID),
		})
		if err != nil {
			// authorize already established the caller is at least a
			// member (or the device's owner); failing to re-read that
			// same membership here would be unexpected, not a real
			// "not found" — but fail closed rather than approving.
			return ErrForbidden
		}
		if m.Role != "owner" && m.Role != "admin" {
			return ErrForbidden
		}
	}
	if err := s.queries.ApproveDevice(ctx, dbtypes.UUID(deviceID)); err != nil {
		return fmt.Errorf("approving device: %w", err)
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "device.approved", TargetType: "device", TargetID: &deviceID,
	})
	return nil
}

func (s *Service) Rename(ctx context.Context, userID, deviceID uuid.UUID, name string) error {
	if _, err := s.authorize(ctx, userID, deviceID); err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("device name is required")
	}
	return s.queries.UpdateDeviceName(ctx, generated.UpdateDeviceNameParams{ID: dbtypes.UUID(deviceID), Name: name})
}

// Revoke marks a device revoked. Any already-issued device access token
// (see AccessTokenIssuer) stops working on its next use because
// DeviceAuthenticate re-checks device status in the database on every
// request — the same live-revocation pattern used for user sessions.
func (s *Service) Revoke(ctx context.Context, userID, deviceID uuid.UUID) error {
	if _, err := s.authorize(ctx, userID, deviceID); err != nil {
		return err
	}
	if err := s.queries.RevokeDevice(ctx, dbtypes.UUID(deviceID)); err != nil {
		return fmt.Errorf("revoking device: %w", err)
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "device.revoked", TargetType: "device", TargetID: &deviceID,
	})
	return nil
}

type Challenge struct {
	ChallengeID uuid.UUID
	Nonce       []byte
}

// IssueChallenge creates a short-lived, single-use random nonce the
// device must sign with its private key to prove possession of it.
// Unauthenticated by design — a device has no user session yet at this
// point, which is exactly the problem device authentication solves.
func (s *Service) IssueChallenge(ctx context.Context, deviceID uuid.UUID) (*Challenge, error) {
	d, err := s.queries.GetDeviceByID(ctx, dbtypes.UUID(deviceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("looking up device: %w", err)
	}
	if d.Status == "revoked" {
		return nil, ErrRevoked
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}

	c, err := s.queries.CreateDeviceChallenge(ctx, generated.CreateDeviceChallengeParams{
		DeviceID:  dbtypes.UUID(deviceID),
		Nonce:     nonce,
		ExpiresAt: dbtypes.Timestamptz(time.Now().Add(challengeTTL)),
	})
	if err != nil {
		return nil, fmt.Errorf("storing challenge: %w", err)
	}

	return &Challenge{ChallengeID: dbtypes.FromUUID(c.ID), Nonce: nonce}, nil
}

// Authenticate verifies that signature is a valid Ed25519 signature over
// the challenge's nonce, produced by the device's registered private key.
// On success it updates last_seen_at and returns the device so the caller
// can issue a device-scoped access token.
func (s *Service) Authenticate(ctx context.Context, deviceID, challengeID uuid.UUID, signature []byte) (*Device, error) {
	d, err := s.queries.GetDeviceByID(ctx, dbtypes.UUID(deviceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("looking up device: %w", err)
	}
	if d.Status == "revoked" {
		return nil, ErrRevoked
	}

	challenge, err := s.queries.GetValidDeviceChallenge(ctx, generated.GetValidDeviceChallengeParams{
		ID: dbtypes.UUID(challengeID), DeviceID: dbtypes.UUID(deviceID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidChallenge
		}
		return nil, fmt.Errorf("looking up challenge: %w", err)
	}

	if !ed25519.Verify(ed25519.PublicKey(d.PublicKey), challenge.Nonce, signature) {
		return nil, ErrInvalidSignature
	}

	if err := s.queries.MarkDeviceChallengeUsed(ctx, challenge.ID); err != nil {
		return nil, fmt.Errorf("marking challenge used: %w", err)
	}
	if err := s.queries.UpdateDeviceLastSeen(ctx, d.ID); err != nil {
		return nil, fmt.Errorf("updating last seen: %w", err)
	}

	userID := dbtypes.FromUUID(d.UserID)
	deviceIDVal := dbtypes.FromUUID(d.ID)
	s.audit.Record(ctx, audit.Event{
		ActorUserID: &userID, EventType: "device.authenticated", TargetType: "device", TargetID: &deviceIDVal,
	})

	result := toDevice(d)
	return &result, nil
}

// Heartbeat updates last_seen_at for an already-authenticated device
// (called by DeviceAuthenticate middleware's caller after verifying the
// device access token and live status).
func (s *Service) Heartbeat(ctx context.Context, deviceID uuid.UUID) error {
	return s.queries.UpdateDeviceLastSeen(ctx, dbtypes.UUID(deviceID))
}

// EncodePublicKey/DecodePublicKey are small helpers so HTTP handlers don't
// each reimplement base64 handling for the raw key bytes.
func EncodePublicKey(key []byte) string { return base64.StdEncoding.EncodeToString(key) }

func DecodePublicKey(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

// ErrNoVaultKey means the device never stored its wrapped vault key
// server-side (it predates this feature, or the person opted out).
var ErrNoVaultKey = errors.New("no wrapped vault key stored for this device")

// ErrInvalidVaultKey rejects a wrapped key that is not the expected
// passphrase-wrapped envelope.
var ErrInvalidVaultKey = errors.New("wrapped_vault_key must be an object with salt, nonce and ciphertext")

// maxWrappedVaultKeyBytes bounds what a client can park in the row; a real
// envelope (salt + nonce + 32-byte key + tag, hex) is well under 1 KiB.
const maxWrappedVaultKeyBytes = 4096

// validateWrappedVaultKey checks the envelope shape only. The server never
// sees the passphrase or the unwrapped key: this blob is already encrypted
// client-side with a key derived from the passphrase (Argon2id), so
// storing it lets the dashboard ask for just the passphrase instead of a
// device.json file. Anyone who steals it still has to guess the passphrase
// offline against Argon2id.
func validateWrappedVaultKey(raw []byte) error {
	if len(raw) == 0 || len(raw) > maxWrappedVaultKeyBytes {
		return ErrInvalidVaultKey
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return ErrInvalidVaultKey
	}
	for _, k := range []string{"salt", "nonce", "ciphertext"} {
		if v, ok := env[k].(string); !ok || v == "" {
			return ErrInvalidVaultKey
		}
	}
	return nil
}

// SetVaultKey stores the device's passphrase-wrapped vault key.
func (s *Service) SetVaultKey(ctx context.Context, userID, deviceID uuid.UUID, raw []byte) error {
	if _, err := s.authorize(ctx, userID, deviceID); err != nil {
		return err
	}
	if err := validateWrappedVaultKey(raw); err != nil {
		return err
	}
	if err := s.queries.SetDeviceWrappedVaultKey(ctx, generated.SetDeviceWrappedVaultKeyParams{
		ID: dbtypes.UUID(deviceID), UserID: dbtypes.UUID(userID), WrappedVaultKey: raw,
	}); err != nil {
		return fmt.Errorf("storing wrapped vault key: %w", err)
	}
	return nil
}

// GetVaultKey returns the stored wrapped vault key, owner only.
func (s *Service) GetVaultKey(ctx context.Context, userID, deviceID uuid.UUID) ([]byte, error) {
	if _, err := s.authorize(ctx, userID, deviceID); err != nil {
		return nil, err
	}
	raw, err := s.queries.GetDeviceWrappedVaultKey(ctx, generated.GetDeviceWrappedVaultKeyParams{
		ID: dbtypes.UUID(deviceID), UserID: dbtypes.UUID(userID),
	})
	if err != nil {
		return nil, fmt.Errorf("reading wrapped vault key: %w", err)
	}
	if len(raw) == 0 {
		return nil, ErrNoVaultKey
	}
	return raw, nil
}
