// Package organizations implements organization creation, membership
// management, and invitations. Every method takes the acting user's ID
// explicitly and re-derives their role from the database — callers (the
// HTTP layer) must never pass a trusted role down into this package, since
// that would let a client forge its own authorization.
package organizations

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/db/generated"
	"deeparchive/internal/audit"
	"deeparchive/internal/dbtypes"
	"deeparchive/internal/mailtemplates"
	"deeparchive/internal/notifications"
	"deeparchive/internal/tokens"
)

var (
	ErrNotFound         = errors.New("organization not found")
	ErrForbidden        = errors.New("forbidden")
	ErrLastOwner        = errors.New("cannot remove or demote the last owner of an organization")
	ErrInvalidRole      = errors.New("invalid role")
	ErrInvalidOrExpired = errors.New("invalid or expired invitation")
	ErrEmailMismatch    = errors.New("invitation email does not match the authenticated account")
)

const invitationTTL = 7 * 24 * time.Hour

type Service struct {
	pool    *pgxpool.Pool
	queries *generated.Queries
	sender  notifications.Sender
	audit   *audit.Recorder
	logger  *slog.Logger
	// WebURL is the web dashboard origin for email links; see identity.Service.
	WebURL string
}

func NewService(pool *pgxpool.Pool, sender notifications.Sender, logger *slog.Logger) *Service {
	queries := generated.New(pool)
	return &Service{
		pool:    pool,
		queries: queries,
		sender:  sender,
		audit:   audit.NewRecorder(queries, logger),
		logger:  logger,
	}
}

type Organization struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	OwnerID   uuid.UUID `json:"owner_id"`
	CreatedAt time.Time `json:"created_at"`
}

func toOrganization(o generated.Organization) Organization {
	return Organization{
		ID:        dbtypes.FromUUID(o.ID),
		Name:      o.Name,
		Slug:      o.Slug,
		OwnerID:   dbtypes.FromUUID(o.OwnerID),
		CreatedAt: dbtypes.FromTimestamptz(o.CreatedAt),
	}
}

// Create creates a new organization owned by userID, with userID as its
// first member in the "owner" role, in a single transaction so an
// organization can never exist without an owner membership row.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, name string) (*Organization, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidRole)
	}
	slug, err := generateSlug(name)
	if err != nil {
		return nil, fmt.Errorf("generating slug: %w", err)
	}

	var org generated.Organization
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		var err error
		org, err = q.CreateOrganization(ctx, generated.CreateOrganizationParams{
			Name:    name,
			Slug:    slug,
			OwnerID: dbtypes.UUID(userID),
		})
		if err != nil {
			return err
		}
		_, err = q.CreateMembership(ctx, generated.CreateMembershipParams{
			OrganizationID: org.ID,
			UserID:         dbtypes.UUID(userID),
			Role:           "owner",
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("creating organization: %w", err)
	}

	s.audit.Record(ctx, audit.Event{
		OrganizationID: ptr(dbtypes.FromUUID(org.ID)), ActorUserID: &userID,
		EventType: "organization.created", TargetType: "organization", TargetID: ptr(dbtypes.FromUUID(org.ID)),
	})

	result := toOrganization(org)
	return &result, nil
}

func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID) ([]Organization, error) {
	rows, err := s.queries.ListOrganizationsForUser(ctx, dbtypes.UUID(userID))
	if err != nil {
		return nil, fmt.Errorf("listing organizations: %w", err)
	}
	out := make([]Organization, 0, len(rows))
	for _, r := range rows {
		out = append(out, toOrganization(r))
	}
	return out, nil
}

// requireMembership returns the caller's role, or ErrNotFound if they are
// not a member. Returning ErrNotFound (rather than ErrForbidden) for
// non-members on read access deliberately avoids confirming an
// organization ID exists to someone outside it.
func (s *Service) requireMembership(ctx context.Context, orgID, userID uuid.UUID) (string, error) {
	m, err := s.queries.GetMembership(ctx, generated.GetMembershipParams{
		OrganizationID: dbtypes.UUID(orgID), UserID: dbtypes.UUID(userID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("looking up membership: %w", err)
	}
	return m.Role, nil
}

func (s *Service) Get(ctx context.Context, userID, orgID uuid.UUID) (*Organization, string, error) {
	role, err := s.requireMembership(ctx, orgID, userID)
	if err != nil {
		return nil, "", err
	}
	org, err := s.queries.GetOrganizationByID(ctx, dbtypes.UUID(orgID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		return nil, "", fmt.Errorf("looking up organization: %w", err)
	}
	result := toOrganization(org)
	return &result, role, nil
}

// UpdateName requires the caller to be an owner or admin.
func (s *Service) UpdateName(ctx context.Context, userID, orgID uuid.UUID, name string) (*Organization, error) {
	role, err := s.requireMembership(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	if role != "owner" && role != "admin" {
		return nil, ErrForbidden
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidRole)
	}
	org, err := s.queries.UpdateOrganizationName(ctx, generated.UpdateOrganizationNameParams{
		ID: dbtypes.UUID(orgID), Name: name,
	})
	if err != nil {
		return nil, fmt.Errorf("updating organization: %w", err)
	}
	s.audit.Record(ctx, audit.Event{
		OrganizationID: &orgID, ActorUserID: &userID,
		EventType: "organization.updated", TargetType: "organization", TargetID: &orgID,
	})
	result := toOrganization(org)
	return &result, nil
}

type Member struct {
	UserID      uuid.UUID `json:"user_id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Service) ListMembers(ctx context.Context, userID, orgID uuid.UUID) ([]Member, error) {
	if _, err := s.requireMembership(ctx, orgID, userID); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListMembersWithUser(ctx, dbtypes.UUID(orgID))
	if err != nil {
		return nil, fmt.Errorf("listing members: %w", err)
	}
	out := make([]Member, 0, len(rows))
	for _, r := range rows {
		out = append(out, Member{
			UserID:      dbtypes.FromUUID(r.UserID),
			Email:       r.Email,
			DisplayName: r.DisplayName,
			Role:        r.Role,
			CreatedAt:   dbtypes.FromTimestamptz(r.CreatedAt),
		})
	}
	return out, nil
}

// Invite requires the caller to be an owner or admin, and only allows
// inviting as admin or member — never owner, since ownership is not
// transferable through the invitation flow (see Leave's doc comment for
// the ownership-transfer limitation).
func (s *Service) Invite(ctx context.Context, userID, orgID uuid.UUID, email, role string) error {
	callerRole, err := s.requireMembership(ctx, orgID, userID)
	if err != nil {
		return err
	}
	if callerRole != "owner" && callerRole != "admin" {
		return ErrForbidden
	}
	if role != "admin" && role != "member" {
		return fmt.Errorf("%w: role must be admin or member", ErrInvalidRole)
	}

	token, err := tokens.Generate()
	if err != nil {
		return fmt.Errorf("generating invitation token: %w", err)
	}
	inv, err := s.queries.CreateInvitation(ctx, generated.CreateInvitationParams{
		OrganizationID: dbtypes.UUID(orgID),
		Email:          email,
		Role:           role,
		TokenHash:      tokens.Hash(token),
		InvitedBy:      dbtypes.UUID(userID),
		ExpiresAt:      dbtypes.Timestamptz(time.Now().Add(invitationTTL)),
	})
	if err != nil {
		return fmt.Errorf("creating invitation: %w", err)
	}

	s.audit.Record(ctx, audit.Event{
		OrganizationID: &orgID, ActorUserID: &userID,
		EventType: "organization.member_invited", TargetType: "invitation", TargetID: ptr(dbtypes.FromUUID(inv.ID)),
		Metadata: map[string]any{"email": email, "role": role},
	})

	msg := mailtemplates.Invite(s.WebURL, token)
	msg.To = email
	return s.sender.Send(ctx, msg)
}

// AcceptInvitation requires the authenticated account's email to match the
// invited email exactly (case-insensitive, enforced by the citext column
// on invitations.email matching users.email) — a signed-in user cannot
// accept an invitation sent to someone else's address.
func (s *Service) AcceptInvitation(ctx context.Context, userID uuid.UUID, userEmail, token string) (*Organization, error) {
	inv, err := s.queries.GetValidInvitationByTokenHash(ctx, tokens.Hash(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidOrExpired
		}
		return nil, fmt.Errorf("looking up invitation: %w", err)
	}
	if !strings.EqualFold(inv.Email, userEmail) {
		return nil, ErrEmailMismatch
	}

	orgID := dbtypes.FromUUID(inv.OrganizationID)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.MarkInvitationAccepted(ctx, inv.ID); err != nil {
			return err
		}
		_, err := q.CreateMembership(ctx, generated.CreateMembershipParams{
			OrganizationID: inv.OrganizationID,
			UserID:         dbtypes.UUID(userID),
			Role:           inv.Role,
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("accepting invitation: %w", err)
	}

	s.audit.Record(ctx, audit.Event{
		OrganizationID: &orgID, ActorUserID: &userID,
		EventType: "organization.member_joined", TargetType: "user", TargetID: &userID,
	})

	org, err := s.queries.GetOrganizationByID(ctx, inv.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("looking up organization: %w", err)
	}
	result := toOrganization(org)
	return &result, nil
}

// UpdateMemberRole requires the caller to be an owner, and refuses to
// demote the last remaining owner (there must always be at least one
// owner able to manage the organization).
func (s *Service) UpdateMemberRole(ctx context.Context, userID, orgID, targetUserID uuid.UUID, newRole string) error {
	callerRole, err := s.requireMembership(ctx, orgID, userID)
	if err != nil {
		return err
	}
	if callerRole != "owner" {
		return ErrForbidden
	}
	if newRole != "owner" && newRole != "admin" && newRole != "member" {
		return fmt.Errorf("%w: invalid role", ErrInvalidRole)
	}

	target, err := s.queries.GetMembership(ctx, generated.GetMembershipParams{
		OrganizationID: dbtypes.UUID(orgID), UserID: dbtypes.UUID(targetUserID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("looking up target membership: %w", err)
	}

	if target.Role == "owner" && newRole != "owner" {
		if err := s.assertNotLastOwner(ctx, orgID); err != nil {
			return err
		}
	}

	if err := s.queries.UpdateMembershipRole(ctx, generated.UpdateMembershipRoleParams{
		OrganizationID: dbtypes.UUID(orgID), UserID: dbtypes.UUID(targetUserID), Role: newRole,
	}); err != nil {
		return fmt.Errorf("updating role: %w", err)
	}

	s.audit.Record(ctx, audit.Event{
		OrganizationID: &orgID, ActorUserID: &userID,
		EventType: "organization.member_role_changed", TargetType: "user", TargetID: &targetUserID,
		Metadata: map[string]any{"new_role": newRole},
	})
	return nil
}

// RemoveMember requires the caller to be an owner or admin. An admin may
// not remove an owner. The last owner cannot be removed.
func (s *Service) RemoveMember(ctx context.Context, userID, orgID, targetUserID uuid.UUID) error {
	callerRole, err := s.requireMembership(ctx, orgID, userID)
	if err != nil {
		return err
	}
	if callerRole != "owner" && callerRole != "admin" {
		return ErrForbidden
	}

	target, err := s.queries.GetMembership(ctx, generated.GetMembershipParams{
		OrganizationID: dbtypes.UUID(orgID), UserID: dbtypes.UUID(targetUserID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("looking up target membership: %w", err)
	}
	if target.Role == "owner" {
		if callerRole != "owner" {
			return ErrForbidden
		}
		if err := s.assertNotLastOwner(ctx, orgID); err != nil {
			return err
		}
	}

	if err := s.queries.DeleteMembership(ctx, generated.DeleteMembershipParams{
		OrganizationID: dbtypes.UUID(orgID), UserID: dbtypes.UUID(targetUserID),
	}); err != nil {
		return fmt.Errorf("removing member: %w", err)
	}

	s.audit.Record(ctx, audit.Event{
		OrganizationID: &orgID, ActorUserID: &userID,
		EventType: "organization.member_removed", TargetType: "user", TargetID: &targetUserID,
	})
	return nil
}

// Leave removes the caller's own membership. A sole owner cannot leave —
// ownership transfer must happen first. This product does not yet
// implement an ownership-transfer flow; a lone owner who wants to leave
// must currently promote another member to owner (UpdateMemberRole) before
// leaving. This is a documented limitation, not an oversight.
func (s *Service) Leave(ctx context.Context, userID, orgID uuid.UUID) error {
	role, err := s.requireMembership(ctx, orgID, userID)
	if err != nil {
		return err
	}
	if role == "owner" {
		if err := s.assertNotLastOwner(ctx, orgID); err != nil {
			return err
		}
	}
	if err := s.queries.DeleteMembership(ctx, generated.DeleteMembershipParams{
		OrganizationID: dbtypes.UUID(orgID), UserID: dbtypes.UUID(userID),
	}); err != nil {
		return fmt.Errorf("leaving organization: %w", err)
	}
	s.audit.Record(ctx, audit.Event{
		OrganizationID: &orgID, ActorUserID: &userID,
		EventType: "organization.member_left", TargetType: "user", TargetID: &userID,
	})
	return nil
}

// lookupUserEmail reads the authenticated user's current email directly
// (bypassing the identity package, which has no exported accessor for
// this) via the shared generated.Queries — users.sql's queries are
// domain-agnostic storage code, not identity-package-private.
func (s *Service) lookupUserEmail(ctx context.Context, userID uuid.UUID) (string, error) {
	user, err := s.queries.GetUserByID(ctx, dbtypes.UUID(userID))
	if err != nil {
		return "", fmt.Errorf("looking up user: %w", err)
	}
	return user.Email, nil
}

func (s *Service) assertNotLastOwner(ctx context.Context, orgID uuid.UUID) error {
	count, err := s.queries.CountOwners(ctx, dbtypes.UUID(orgID))
	if err != nil {
		return fmt.Errorf("counting owners: %w", err)
	}
	if count <= 1 {
		return ErrLastOwner
	}
	return nil
}

var slugInvalidChars = regexp.MustCompile(`[^a-z0-9-]+`)

func generateSlug(name string) (string, error) {
	base := strings.ToLower(strings.TrimSpace(name))
	base = slugInvalidChars.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "org"
	}
	if len(base) > 48 {
		base = base[:48]
	}
	suffix, err := randomSuffix()
	if err != nil {
		return "", err
	}
	return base + "-" + suffix, nil
}

func randomSuffix() (string, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

func ptr(u uuid.UUID) *uuid.UUID { return &u }

// --- Operator/admin methods -------------------------------------------
//
// Called only by backend/cmd/deepctl — see internal/identity's matching
// section for why these skip the usual per-call authorization check.

// AdminOrganization's JSON tags matter now that this also crosses the
// HTTP API (the admin panel), not just deepctl's plain-text tables —
// snake_case to match every other response shape in this API.
type AdminOrganization struct {
	ID                     uuid.UUID `json:"id"`
	Name                   string    `json:"name"`
	Slug                   string    `json:"slug"`
	OwnerID                uuid.UUID `json:"owner_id"`
	StorageQuotaBytes      int64     `json:"storage_quota_bytes"`
	RetentionKeepSnapshots int32     `json:"retention_keep_snapshots"`
	CreatedAt              time.Time `json:"created_at"`
}

func toAdminOrganization(o generated.Organization) AdminOrganization {
	return AdminOrganization{
		ID:                     dbtypes.FromUUID(o.ID),
		Name:                   o.Name,
		Slug:                   o.Slug,
		OwnerID:                dbtypes.FromUUID(o.OwnerID),
		StorageQuotaBytes:      o.StorageQuotaBytes,
		RetentionKeepSnapshots: o.RetentionKeepSnapshots,
		CreatedAt:              dbtypes.FromTimestamptz(o.CreatedAt),
	}
}

func (s *Service) AdminListOrganizations(ctx context.Context, limit, offset int32) ([]AdminOrganization, error) {
	rows, err := s.queries.ListOrganizationsPage(ctx, generated.ListOrganizationsPageParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("listing organizations: %w", err)
	}
	out := make([]AdminOrganization, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAdminOrganization(r))
	}
	return out, nil
}

func (s *Service) AdminGetByID(ctx context.Context, orgID uuid.UUID) (*AdminOrganization, error) {
	o, err := s.queries.GetOrganizationByID(ctx, dbtypes.UUID(orgID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("looking up organization: %w", err)
	}
	result := toAdminOrganization(o)
	return &result, nil
}

// AdminSearchOrganizations is AdminListOrganizations plus an optional
// substring filter on name/slug, with a matching total count for
// pagination — what the web admin panel's search box needs.
func (s *Service) AdminSearchOrganizations(ctx context.Context, query string, limit, offset int32) ([]AdminOrganization, int64, error) {
	total, err := s.queries.CountOrganizationsMatching(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("counting organizations: %w", err)
	}
	rows, err := s.queries.SearchOrganizationsPage(ctx, generated.SearchOrganizationsPageParams{
		Column1: query, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("searching organizations: %w", err)
	}
	out := make([]AdminOrganization, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAdminOrganization(r))
	}
	return out, total, nil
}

// AdminListMembers is ListMembers without the caller-membership check —
// deepctl's/the admin panel's trust boundary is "already authorized as an
// operator/admin", not "already a member of this specific org".
func (s *Service) AdminListMembers(ctx context.Context, orgID uuid.UUID) ([]Member, error) {
	rows, err := s.queries.ListMembersWithUser(ctx, dbtypes.UUID(orgID))
	if err != nil {
		return nil, fmt.Errorf("listing members: %w", err)
	}
	out := make([]Member, 0, len(rows))
	for _, r := range rows {
		out = append(out, Member{
			UserID:      dbtypes.FromUUID(r.UserID),
			Email:       r.Email,
			DisplayName: r.DisplayName,
			Role:        r.Role,
			CreatedAt:   dbtypes.FromTimestamptz(r.CreatedAt),
		})
	}
	return out, nil
}

// AdminListOrganizationsForUser lists every organization a user belongs
// to (name + id only) — what the admin panel's user-detail page shows,
// reusing the same query ListForUser is built on rather than re-deriving
// an admin-only version of it.
func (s *Service) AdminListOrganizationsForUser(ctx context.Context, userID uuid.UUID) ([]Organization, error) {
	rows, err := s.queries.ListOrganizationsForUser(ctx, dbtypes.UUID(userID))
	if err != nil {
		return nil, fmt.Errorf("listing organizations: %w", err)
	}
	out := make([]Organization, 0, len(rows))
	for _, r := range rows {
		out = append(out, toOrganization(r))
	}
	return out, nil
}

func (s *Service) AdminSetStorageQuota(ctx context.Context, orgID uuid.UUID, bytes int64) error {
	if err := s.queries.UpdateOrganizationStorageQuota(ctx, generated.UpdateOrganizationStorageQuotaParams{
		ID: dbtypes.UUID(orgID), StorageQuotaBytes: bytes,
	}); err != nil {
		return fmt.Errorf("updating storage quota: %w", err)
	}
	s.audit.Record(ctx, audit.Event{
		OrganizationID: ptr(orgID), EventType: "admin.org_storage_quota_set", TargetType: "organization", TargetID: ptr(orgID),
		Metadata: map[string]any{"storage_quota_bytes": bytes},
	})
	return nil
}

func (s *Service) AdminSetRetentionLimit(ctx context.Context, orgID uuid.UUID, keep int32) error {
	if err := s.queries.UpdateOrganizationRetentionLimit(ctx, generated.UpdateOrganizationRetentionLimitParams{
		ID: dbtypes.UUID(orgID), RetentionKeepSnapshots: keep,
	}); err != nil {
		return fmt.Errorf("updating retention limit: %w", err)
	}
	s.audit.Record(ctx, audit.Event{
		OrganizationID: ptr(orgID), EventType: "admin.org_retention_limit_set", TargetType: "organization", TargetID: ptr(orgID),
		Metadata: map[string]any{"retention_keep_snapshots": keep},
	})
	return nil
}
