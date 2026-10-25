// Package quotas enforces the per-scope storage limit set by
// users.storage_quota_bytes / organizations.storage_quota_bytes. It is
// checked, not merely advisory: internal/backups.Service.CommitSnapshot
// calls EnsureCapacity before persisting any new chunk row, so a commit
// that would exceed quota fails outright rather than partially writing
// state and leaving a scope over its limit.
package quotas

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/db/generated"
	"deeparchive/internal/dbtypes"
	"deeparchive/internal/usage"
)

var ErrQuotaExceeded = errors.New("storage quota exceeded")

type Service struct {
	queries *generated.Queries
	usage   *usage.Service
}

func NewService(pool *pgxpool.Pool, usageSvc *usage.Service) *Service {
	return &Service{queries: generated.New(pool), usage: usageSvc}
}

// EnsureCapacity returns ErrQuotaExceeded if adding additionalBytes to the
// given scope (an organization if orgID is non-nil, otherwise the
// individual user) would exceed that scope's storage_quota_bytes. The
// scope here is deliberately the same one dedup uses — see
// backend/db/migrations/0004_backups.up.sql — so "how much a device may
// still upload" always matches "what it would actually be billed/limited
// against."
func (s *Service) EnsureCapacity(ctx context.Context, userID uuid.UUID, orgID *uuid.UUID, additionalBytes int64) error {
	if orgID != nil {
		org, err := s.queries.GetOrganizationByID(ctx, dbtypes.UUID(*orgID))
		if err != nil {
			return fmt.Errorf("looking up organization: %w", err)
		}
		used, err := s.usage.ForOrganization(ctx, *orgID)
		if err != nil {
			return err
		}
		if used+additionalBytes > org.StorageQuotaBytes {
			return fmt.Errorf("%w: organization has used %d of %d bytes and this backup needs %d more",
				ErrQuotaExceeded, used, org.StorageQuotaBytes, additionalBytes)
		}
		return nil
	}

	user, err := s.queries.GetUserByID(ctx, dbtypes.UUID(userID))
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	used, err := s.usage.ForUser(ctx, userID)
	if err != nil {
		return err
	}
	if used+additionalBytes > user.StorageQuotaBytes {
		return fmt.Errorf("%w: account has used %d of %d bytes and this backup needs %d more",
			ErrQuotaExceeded, used, user.StorageQuotaBytes, additionalBytes)
	}
	return nil
}
