// Package retention enforces each account's snapshot retention limit:
// for every device that has committed at least one snapshot, only the
// most recent N (the device's owning user's or organization's
// retention_keep_snapshots) are kept — anything older is deleted,
// including garbage-collecting any chunk that deletion leaves with no
// remaining reference. See internal/backups.Service.DeleteSnapshotForRetention
// for the actual deletion/GC mechanics; this package only decides which
// snapshots are old enough to go.
package retention

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/db/generated"
	"deeparchive/internal/backups"
	"deeparchive/internal/dbtypes"
)

type Service struct {
	queries *generated.Queries
	backups *backups.Service
	logger  *slog.Logger
}

func NewService(pool *pgxpool.Pool, backupsSvc *backups.Service, logger *slog.Logger) *Service {
	return &Service{queries: generated.New(pool), backups: backupsSvc, logger: logger}
}

// Sweep runs one full retention pass over every device that has ever
// committed a snapshot. A failure sweeping one device (a transient DB or
// storage error) is logged and skipped rather than aborting the whole
// pass — one bad device must not starve retention for every other
// account. Returns how many snapshots were actually deleted, for
// logging/testing visibility.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	deviceIDs, err := s.queries.ListDistinctDeviceIDsWithSnapshots(ctx)
	if err != nil {
		return 0, fmt.Errorf("listing devices with snapshots: %w", err)
	}

	deleted := 0
	for _, raw := range deviceIDs {
		deviceID := dbtypes.FromUUID(raw)
		n, err := s.sweepDevice(ctx, deviceID)
		if err != nil {
			s.logger.Error("retention_sweep_device_failed", "device_id", deviceID, "error", err)
			continue
		}
		deleted += n
	}
	return deleted, nil
}

func (s *Service) sweepDevice(ctx context.Context, deviceID uuid.UUID) (int, error) {
	device, err := s.queries.GetDeviceByID(ctx, dbtypes.UUID(deviceID))
	if err != nil {
		return 0, fmt.Errorf("looking up device: %w", err)
	}

	limit, err := s.retentionLimit(ctx, device)
	if err != nil {
		return 0, err
	}

	// Ordered most-recent-first (see ListSnapshotsForDevice), so keeping
	// "the first `limit`" means keeping the newest ones.
	snapshots, err := s.queries.ListSnapshotsForDevice(ctx, dbtypes.UUID(deviceID))
	if err != nil {
		return 0, fmt.Errorf("listing snapshots: %w", err)
	}
	if len(snapshots) <= int(limit) {
		return 0, nil
	}

	deleted := 0
	for _, snap := range snapshots[limit:] {
		snapID := dbtypes.FromUUID(snap.ID)
		if err := s.backups.DeleteSnapshotForRetention(ctx, snapID); err != nil {
			s.logger.Error("retention_delete_failed", "snapshot_id", snapID, "error", err)
			continue
		}
		deleted++
	}
	return deleted, nil
}

func (s *Service) retentionLimit(ctx context.Context, d generated.Device) (int32, error) {
	if d.OrganizationID.Valid {
		org, err := s.queries.GetOrganizationByID(ctx, d.OrganizationID)
		if err != nil {
			return 0, fmt.Errorf("looking up organization: %w", err)
		}
		return org.RetentionKeepSnapshots, nil
	}
	user, err := s.queries.GetUserByID(ctx, d.UserID)
	if err != nil {
		return 0, fmt.Errorf("looking up user: %w", err)
	}
	return user.RetentionKeepSnapshots, nil
}
