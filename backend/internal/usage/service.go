// Package usage computes current storage usage from the same source of
// truth the dedup scope itself uses — a live SUM over chunks.ciphertext_size
// — rather than maintaining a separately-updated running counter that could
// drift from reality. Fine at this project's scale; revisit with a
// materialized/cached counter if this aggregate ever shows up as a hot path.
package usage

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/db/generated"
	"deeparchive/internal/dbtypes"
)

type Service struct {
	queries *generated.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{queries: generated.New(pool)}
}

// ForUser returns bytes of ciphertext stored in userID's individual dedup
// scope (chunks with no organization_id) — see
// backend/db/migrations/0004_backups.up.sql's dedup-scope comment.
func (s *Service) ForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	total, err := s.queries.SumChunkSizeForUser(ctx, dbtypes.UUID(userID))
	if err != nil {
		return 0, fmt.Errorf("summing user chunk usage: %w", err)
	}
	return total, nil
}

// ForOrganization returns bytes of ciphertext stored in orgID's shared
// dedup scope.
func (s *Service) ForOrganization(ctx context.Context, orgID uuid.UUID) (int64, error) {
	total, err := s.queries.SumChunkSizeForOrg(ctx, dbtypes.UUID(orgID))
	if err != nil {
		return 0, fmt.Errorf("summing organization chunk usage: %w", err)
	}
	return total, nil
}

// AdminTotal returns storage usage summed across every user and
// organization — the admin panel's dashboard overview, not scoped to any
// one caller.
func (s *Service) AdminTotal(ctx context.Context) (int64, error) {
	total, err := s.queries.SumChunkSizeTotal(ctx)
	if err != nil {
		return 0, fmt.Errorf("summing total chunk usage: %w", err)
	}
	return total, nil
}

type ScopeUsage struct {
	UsedBytes  int64 `json:"used_bytes"`
	QuotaBytes int64 `json:"quota_bytes"`
}

type OrganizationUsage struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	Name           string    `json:"name"`
	ScopeUsage
}

type Report struct {
	Personal      ScopeUsage          `json:"personal"`
	Organizations []OrganizationUsage `json:"organizations"`
}

// Report returns userID's own usage/quota plus usage/quota for every
// organization they belong to — everything the web dashboard's storage
// page needs in one call, reusing the same generated queries the
// organizations package's own ListForUser uses rather than depending on
// that package directly.
func (s *Service) Report(ctx context.Context, userID uuid.UUID) (*Report, error) {
	user, err := s.queries.GetUserByID(ctx, dbtypes.UUID(userID))
	if err != nil {
		return nil, fmt.Errorf("looking up user: %w", err)
	}
	personalUsed, err := s.ForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	orgs, err := s.queries.ListOrganizationsForUser(ctx, dbtypes.UUID(userID))
	if err != nil {
		return nil, fmt.Errorf("listing organizations: %w", err)
	}
	orgUsages := make([]OrganizationUsage, 0, len(orgs))
	for _, o := range orgs {
		used, err := s.ForOrganization(ctx, dbtypes.FromUUID(o.ID))
		if err != nil {
			return nil, err
		}
		orgUsages = append(orgUsages, OrganizationUsage{
			OrganizationID: dbtypes.FromUUID(o.ID),
			Name:           o.Name,
			ScopeUsage:     ScopeUsage{UsedBytes: used, QuotaBytes: o.StorageQuotaBytes},
		})
	}

	return &Report{
		Personal:      ScopeUsage{UsedBytes: personalUsed, QuotaBytes: user.StorageQuotaBytes},
		Organizations: orgUsages,
	}, nil
}
