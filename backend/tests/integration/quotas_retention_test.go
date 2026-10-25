//go:build integration

// Integration tests for Milestone 4's storage quota enforcement and
// snapshot retention, against a real disposable Postgres AND real MinIO.
// These directly exercise the two properties that only mean anything
// against the real thing: a commit that would exceed quota must not leave
// ANY new chunk row behind, and retention's garbage collection must
// actually remove the object from MinIO, not just the database row.
package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"deeparchive/internal/backups"
	"deeparchive/internal/quotas"
	"deeparchive/internal/storage"
)

func setStorageQuota(t *testing.T, env *backupsTestEnv, userID string, bytes int64) {
	t.Helper()
	if _, err := env.pool.Exec(context.Background(),
		"UPDATE users SET storage_quota_bytes = $1 WHERE id = $2", bytes, userID); err != nil {
		t.Fatalf("setting storage quota: %v", err)
	}
}

func setRetentionKeepSnapshots(t *testing.T, env *backupsTestEnv, userID string, n int) {
	t.Helper()
	if _, err := env.pool.Exec(context.Background(),
		"UPDATE users SET retention_keep_snapshots = $1 WHERE id = $2", n, userID); err != nil {
		t.Fatalf("setting retention limit: %v", err)
	}
}

func TestQuotas_CommitOverQuotaIsRejectedAndLeavesNoNewChunk(t *testing.T) {
	env, _ := newBackupsTestEnv(t)
	ctx := context.Background()
	userID, deviceID := env.registerDevice(t)
	setStorageQuota(t, env, userID.String(), 10) // 10 bytes — nothing will fit

	plaintext := []byte("this content is much longer than ten bytes")
	hash := contentHash(plaintext)
	statuses, err := env.svc.NegotiateUpload(ctx, deviceID, []string{hash})
	if err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	httpPut(t, statuses[0].UploadURL, plaintext)

	_, err = env.svc.CommitSnapshot(ctx, backups.CommitSnapshotParams{
		DeviceID: deviceID, ManifestNonce: []byte("n"), ManifestCiphertext: []byte("c"),
		TotalSize: int64(len(plaintext)), Chunks: []backups.ChunkInput{{Hash: hash, CiphertextSize: int64(len(plaintext))}},
	})
	if !errors.Is(err, quotas.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}

	// The chunk was verified to exist in storage (the negotiate+upload
	// above was real) but quota must have been checked BEFORE any row was
	// written — re-negotiating should still say "missing," not "already
	// have it," because no chunk row exists.
	statuses, err = env.svc.NegotiateUpload(ctx, deviceID, []string{hash})
	if err != nil {
		t.Fatalf("re-negotiate: %v", err)
	}
	if statuses[0].Exists {
		t.Fatal("a chunk row was created despite the commit being rejected for quota")
	}
}

func TestQuotas_CommitWithinQuotaSucceeds(t *testing.T) {
	env, _ := newBackupsTestEnv(t)
	ctx := context.Background()
	userID, deviceID := env.registerDevice(t)
	setStorageQuota(t, env, userID.String(), 1024*1024) // 1 MiB — plenty

	plaintext := []byte("small content, comfortably within quota")
	hash := contentHash(plaintext)
	statuses, err := env.svc.NegotiateUpload(ctx, deviceID, []string{hash})
	if err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	httpPut(t, statuses[0].UploadURL, plaintext)

	if _, err := env.svc.CommitSnapshot(ctx, backups.CommitSnapshotParams{
		DeviceID: deviceID, ManifestNonce: []byte("n"), ManifestCiphertext: []byte("c"),
		TotalSize: int64(len(plaintext)), Chunks: []backups.ChunkInput{{Hash: hash, CiphertextSize: int64(len(plaintext))}},
	}); err != nil {
		t.Fatalf("expected commit within quota to succeed, got %v", err)
	}
}

// commitOneSnapshot is a small helper for the retention tests below: each
// snapshot needs its own unique chunk so garbage collection has something
// distinguishable to decide about per snapshot.
func commitOneSnapshot(t *testing.T, env *backupsTestEnv, deviceID uuid.UUID, uniqueContent string) (snapshotID, objectKey string) {
	t.Helper()
	ctx := context.Background()
	plaintext := []byte(uniqueContent)
	hash := contentHash(plaintext)
	statuses, err := env.svc.NegotiateUpload(ctx, deviceID, []string{hash})
	if err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	httpPut(t, statuses[0].UploadURL, plaintext)
	snap, err := env.svc.CommitSnapshot(ctx, backups.CommitSnapshotParams{
		DeviceID: deviceID, ManifestNonce: []byte("n"), ManifestCiphertext: []byte("c"),
		TotalSize: int64(len(plaintext)), Chunks: []backups.ChunkInput{{Hash: hash, CiphertextSize: int64(len(plaintext))}},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return snap.ID.String(), statuses[0].ObjectKey
}

func TestRetention_SweepDeletesOldestSnapshotsAndGarbageCollectsTheirChunks(t *testing.T) {
	env, _ := newBackupsTestEnv(t)
	ctx := context.Background()
	userID, deviceID := env.registerDevice(t)
	setStorageQuota(t, env, userID.String(), 1024*1024)
	setRetentionKeepSnapshots(t, env, userID.String(), 2)

	_, oldestObjectKey := commitOneSnapshot(t, env, deviceID, "snapshot 1 content, unique")
	commitOneSnapshot(t, env, deviceID, "snapshot 2 content, unique")
	commitOneSnapshot(t, env, deviceID, "snapshot 3 content, unique")

	list, err := env.svc.ListForDevice(ctx, userID, deviceID)
	if err != nil {
		t.Fatalf("list before sweep: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 snapshots before sweep, got %d", len(list))
	}

	deleted, err := env.retention.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if deleted < 1 {
		t.Fatalf("expected at least 1 snapshot deleted by sweep, got %d", deleted)
	}

	list, err = env.svc.ListForDevice(ctx, userID, deviceID)
	if err != nil {
		t.Fatalf("list after sweep: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected exactly 2 snapshots to remain (retention_keep_snapshots=2), got %d", len(list))
	}

	// The real point of retention: the oldest snapshot's chunk must be
	// gone from object storage, not just unlinked in the database.
	if _, err := env.storage.Stat(ctx, oldestObjectKey); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected the oldest snapshot's chunk to be removed from object storage, got %v", err)
	}
}

func TestRetention_SweepLeavesAnAccountUnderItsLimitAlone(t *testing.T) {
	env, _ := newBackupsTestEnv(t)
	ctx := context.Background()
	userID, deviceID := env.registerDevice(t)
	setStorageQuota(t, env, userID.String(), 1024*1024)
	setRetentionKeepSnapshots(t, env, userID.String(), 10)

	commitOneSnapshot(t, env, deviceID, "only snapshot, well under the retention limit")

	if _, err := env.retention.Sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	list, err := env.svc.ListForDevice(ctx, userID, deviceID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected the single snapshot to survive a sweep under the retention limit, got %d remaining", len(list))
	}
}
