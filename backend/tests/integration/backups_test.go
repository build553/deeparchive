//go:build integration

// Integration tests for internal/backups against a real, disposable
// PostgreSQL database AND real MinIO (not mocks) — the property this
// package most needs proven for real is "a snapshot cannot be committed
// referencing a chunk that was never actually uploaded," which only means
// anything against a real object store. Run with `make test-integration`
// (brings up postgres + minio + minio-init first).
package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/internal/backups"
	"deeparchive/internal/config"
	"deeparchive/internal/devices"
	"deeparchive/internal/identity"
	"deeparchive/internal/quotas"
	"deeparchive/internal/retention"
	"deeparchive/internal/storage"
	"deeparchive/internal/usage"
)

type backupsTestEnv struct {
	svc       *backups.Service
	devSvc    *devices.Service
	retention *retention.Service
	storage   *storage.Client
	pool      *pgxpool.Pool
	ident     *testEnv
}

func newBackupsTestEnv(t *testing.T) (*backupsTestEnv, *pgxpool.Pool) {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connecting to database: %v", err)
	}
	t.Cleanup(pool.Close)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	storageClient, err := storage.NewClient(config.S3Config{
		Endpoint:        getenvDefault("S3_ENDPOINT", "http://localhost:9000"),
		Region:          getenvDefault("S3_REGION", "us-east-1"),
		Bucket:          getenvDefault("S3_BUCKET", "deeparchive-dev"),
		AccessKeyID:     getenvDefault("S3_ACCESS_KEY_ID", "deeparchive-dev"),
		SecretAccessKey: getenvDefault("S3_SECRET_ACCESS_KEY", "deeparchive-dev-secret"),
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatalf("creating storage client: %v", err)
	}

	usageSvc := usage.NewService(pool)
	quotasSvc := quotas.NewService(pool, usageSvc)
	backupsSvc := backups.NewService(pool, storageClient, quotasSvc, logger)

	identEnv := newTestEnv(t)
	return &backupsTestEnv{
		svc:       backupsSvc,
		devSvc:    devices.NewService(pool, logger),
		retention: retention.NewService(pool, backupsSvc, logger),
		storage:   storageClient,
		pool:      pool,
		ident:     identEnv,
	}, pool
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// registerDevice registers and logs in a fresh user, enrolls and approves
// a device for them, and returns the user and device IDs — the common
// setup every test below needs before it can exercise backups.
func (e *backupsTestEnv) registerDevice(t *testing.T) (userID, deviceID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	email := uniqueEmail(t)
	if err := e.ident.registerVerified(ctx, email, testPassword, "Backups User"); err != nil {
		t.Fatalf("register: %v", err)
	}
	session, err := e.ident.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	userID = e.ident.userID(t, session.AccessToken)

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating ed25519 key: %v", err)
	}
	d, err := e.devSvc.Enroll(ctx, devices.EnrollParams{
		UserID: userID, Name: "Test Laptop", OS: "linux", ClientVersion: "0.1.0", PublicKey: pub,
	})
	if err != nil {
		t.Fatalf("enroll device: %v", err)
	}
	if err := e.devSvc.Approve(ctx, userID, d.ID); err != nil {
		t.Fatalf("approve device: %v", err)
	}
	return userID, d.ID
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func httpPut(t *testing.T, url string, body []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building PUT request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT %s: status %d: %s", url, resp.StatusCode, b)
	}
}

func httpGet(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading GET %s body: %v", url, err)
	}
	if resp.StatusCode/100 != 2 {
		t.Fatalf("GET %s: status %d: %s", url, resp.StatusCode, b)
	}
	return b
}

func TestBackups_NegotiateUploadCommitRestore_FullFlow(t *testing.T) {
	env, _ := newBackupsTestEnv(t)
	ctx := context.Background()
	userID, deviceID := env.registerDevice(t)

	plaintext := []byte("some chunk content for the full flow test")
	hash := contentHash(plaintext)

	statuses, err := env.svc.NegotiateUpload(ctx, deviceID, []string{hash})
	if err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Exists || statuses[0].UploadURL == "" {
		t.Fatalf("expected a new chunk with an upload URL, got %+v", statuses)
	}
	httpPut(t, statuses[0].UploadURL, plaintext)

	snap, err := env.svc.CommitSnapshot(ctx, backups.CommitSnapshotParams{
		DeviceID:           deviceID,
		ManifestNonce:      []byte("fake-nonce-12"),
		ManifestCiphertext: []byte("fake encrypted manifest bytes"),
		TotalSize:          int64(len(plaintext)),
		Chunks:             []backups.ChunkInput{{Hash: hash, CiphertextSize: int64(len(plaintext))}},
	})
	if err != nil {
		t.Fatalf("commit snapshot: %v", err)
	}
	if snap.ChunkCount != 1 || snap.UserID != userID {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}

	// Negotiating the same hash again must report it as already known —
	// this is the whole point of content-addressed dedup.
	statuses, err = env.svc.NegotiateUpload(ctx, deviceID, []string{hash})
	if err != nil {
		t.Fatalf("re-negotiate: %v", err)
	}
	if !statuses[0].Exists || statuses[0].UploadURL != "" {
		t.Fatalf("expected the chunk to already exist with no upload URL, got %+v", statuses)
	}

	detail, err := env.svc.Get(ctx, userID, snap.ID)
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if len(detail.Chunks) != 1 || detail.Chunks[0].Hash != hash {
		t.Fatalf("unexpected snapshot detail chunks: %+v", detail.Chunks)
	}
	downloaded := httpGet(t, detail.Chunks[0].DownloadURL)
	if !bytes.Equal(downloaded, plaintext) {
		t.Fatalf("downloaded chunk does not match uploaded content: got %q, want %q", downloaded, plaintext)
	}

	list, err := env.svc.ListForUser(ctx, userID)
	if err != nil {
		t.Fatalf("list for user: %v", err)
	}
	if len(list) != 1 || list[0].ID != snap.ID {
		t.Fatalf("expected exactly the one committed snapshot, got %+v", list)
	}
}

func TestBackups_CommitRejectsAChunkThatWasNeverUploaded(t *testing.T) {
	env, _ := newBackupsTestEnv(t)
	ctx := context.Background()
	_, deviceID := env.registerDevice(t)

	hash := contentHash([]byte("content nobody ever uploaded"))
	_, err := env.svc.CommitSnapshot(ctx, backups.CommitSnapshotParams{
		DeviceID:           deviceID,
		ManifestNonce:      []byte("n"),
		ManifestCiphertext: []byte("c"),
		TotalSize:          1,
		Chunks:             []backups.ChunkInput{{Hash: hash, CiphertextSize: 1}},
	})
	if !errors.Is(err, backups.ErrChunkNotUploaded) {
		t.Fatalf("expected ErrChunkNotUploaded, got %v", err)
	}
}

func TestBackups_DedupIsScopedPerUser_NotGlobal(t *testing.T) {
	env, _ := newBackupsTestEnv(t)
	ctx := context.Background()
	_, deviceA := env.registerDevice(t)
	_, deviceB := env.registerDevice(t)

	plaintext := []byte("identical content uploaded by two unrelated users")
	hash := contentHash(plaintext)

	statusesA, err := env.svc.NegotiateUpload(ctx, deviceA, []string{hash})
	if err != nil {
		t.Fatalf("negotiate for user A: %v", err)
	}
	httpPut(t, statusesA[0].UploadURL, plaintext)
	if _, err := env.svc.CommitSnapshot(ctx, backups.CommitSnapshotParams{
		DeviceID: deviceA, ManifestNonce: []byte("n"), ManifestCiphertext: []byte("c"),
		TotalSize: int64(len(plaintext)), Chunks: []backups.ChunkInput{{Hash: hash, CiphertextSize: int64(len(plaintext))}},
	}); err != nil {
		t.Fatalf("commit for user A: %v", err)
	}

	// User B's device negotiating the SAME content hash must still be told
	// it's missing: a different user is a different dedup scope, and dedup
	// leaking across scopes would mean user B could learn "user A already
	// has this exact content" — see migration 0004_backups's comment.
	statusesB, err := env.svc.NegotiateUpload(ctx, deviceB, []string{hash})
	if err != nil {
		t.Fatalf("negotiate for user B: %v", err)
	}
	if statusesB[0].Exists {
		t.Fatalf("dedup leaked across users: user B was told a chunk it never uploaded already exists")
	}
}

func TestBackups_OtherUserCannotReadSnapshot(t *testing.T) {
	env, _ := newBackupsTestEnv(t)
	ctx := context.Background()
	_, deviceID := env.registerDevice(t)
	outsiderID, _ := env.registerDevice(t)

	plaintext := []byte("owner-only content")
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
	if _, err := env.svc.Get(ctx, outsiderID, snap.ID); !errors.Is(err, backups.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for an outsider reading another user's snapshot, got %v", err)
	}
	list, err := env.svc.ListForUser(ctx, outsiderID)
	if err != nil {
		t.Fatalf("list for outsider: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected the outsider to see no snapshots, got %+v", list)
	}
}
