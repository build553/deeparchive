//go:build integration

package integration

import (
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/internal/devices"
	"deeparchive/internal/identity"
	"deeparchive/internal/organizations"
)

func newDeviceTestEnv(t *testing.T) (*devices.Service, *testEnv) {
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
	identEnv := newTestEnv(t)
	devSvc := devices.NewService(pool, logger)
	return devSvc, identEnv
}

func newDeviceOrgTestEnv(t *testing.T) (*devices.Service, *organizations.Service, *testEnv) {
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
	identEnv := newTestEnv(t)
	devSvc := devices.NewService(pool, logger)
	orgSvc := organizations.NewService(pool, identEnv.sender, logger)
	return devSvc, orgSvc, identEnv
}

func TestDevices_EnrollAndAuthenticate_FullChallengeResponseFlow(t *testing.T) {
	devSvc, identEnv := newDeviceTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := identEnv.registerVerified(ctx, email, testPassword, "Device User"); err != nil {
		t.Fatalf("register: %v", err)
	}
	session, err := identEnv.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	userID := identEnv.userID(t, session.AccessToken)

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating ed25519 key: %v", err)
	}

	d, err := devSvc.Enroll(ctx, devices.EnrollParams{
		UserID: userID, Name: "Test Laptop", OS: "linux", ClientVersion: "0.1.0", PublicKey: pub,
	})
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if d.Status != "pending" {
		t.Fatalf("expected new device to be pending, got %q", d.Status)
	}

	if err := devSvc.Approve(ctx, userID, d.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	challenge, err := devSvc.IssueChallenge(ctx, d.ID)
	if err != nil {
		t.Fatalf("issue challenge: %v", err)
	}

	signature := ed25519.Sign(priv, challenge.Nonce)

	authenticated, err := devSvc.Authenticate(ctx, d.ID, challenge.ChallengeID, signature)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if authenticated.ID != d.ID {
		t.Fatalf("expected authenticated device id %s, got %s", d.ID, authenticated.ID)
	}

	// The same challenge cannot be replayed.
	if _, err := devSvc.Authenticate(ctx, d.ID, challenge.ChallengeID, signature); err == nil {
		t.Fatal("expected reused challenge to be rejected")
	}
}

func TestDevices_AuthenticateWithWrongKeyFails(t *testing.T) {
	devSvc, identEnv := newDeviceTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := identEnv.registerVerified(ctx, email, testPassword, "Device User"); err != nil {
		t.Fatalf("register: %v", err)
	}
	session, err := identEnv.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	userID := identEnv.userID(t, session.AccessToken)

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating ed25519 key: %v", err)
	}
	_, wrongPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating second ed25519 key: %v", err)
	}

	d, err := devSvc.Enroll(ctx, devices.EnrollParams{
		UserID: userID, Name: "Test Laptop", OS: "linux", ClientVersion: "0.1.0", PublicKey: pub,
	})
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}

	challenge, err := devSvc.IssueChallenge(ctx, d.ID)
	if err != nil {
		t.Fatalf("issue challenge: %v", err)
	}

	// Sign with a DIFFERENT private key than the one registered.
	wrongSignature := ed25519.Sign(wrongPriv, challenge.Nonce)
	if _, err := devSvc.Authenticate(ctx, d.ID, challenge.ChallengeID, wrongSignature); err != devices.ErrInvalidSignature {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestDevices_RevokedDeviceCannotAuthenticate(t *testing.T) {
	devSvc, identEnv := newDeviceTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := identEnv.registerVerified(ctx, email, testPassword, "Device User"); err != nil {
		t.Fatalf("register: %v", err)
	}
	session, err := identEnv.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	userID := identEnv.userID(t, session.AccessToken)

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating ed25519 key: %v", err)
	}
	d, err := devSvc.Enroll(ctx, devices.EnrollParams{
		UserID: userID, Name: "Test Laptop", OS: "linux", ClientVersion: "0.1.0", PublicKey: pub,
	})
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if err := devSvc.Revoke(ctx, userID, d.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if _, err := devSvc.IssueChallenge(ctx, d.ID); err != devices.ErrRevoked {
		t.Fatalf("expected ErrRevoked issuing a challenge for a revoked device, got %v", err)
	}
}

func TestDevices_OtherUserCannotSeeOrManageDevice(t *testing.T) {
	devSvc, identEnv := newDeviceTestEnv(t)
	ctx := context.Background()

	ownerEmail := "owner-" + uniqueEmail(t)
	outsiderEmail := "outsider-" + uniqueEmail(t)
	if err := identEnv.registerVerified(ctx, ownerEmail, testPassword, "Owner"); err != nil {
		t.Fatalf("register owner: %v", err)
	}
	if err := identEnv.registerVerified(ctx, outsiderEmail, testPassword, "Outsider"); err != nil {
		t.Fatalf("register outsider: %v", err)
	}
	ownerSession, err := identEnv.svc.Login(ctx, identity.LoginParams{Email: ownerEmail, Password: testPassword})
	if err != nil {
		t.Fatalf("login owner: %v", err)
	}
	outsiderSession, err := identEnv.svc.Login(ctx, identity.LoginParams{Email: outsiderEmail, Password: testPassword})
	if err != nil {
		t.Fatalf("login outsider: %v", err)
	}
	ownerID := identEnv.userID(t, ownerSession.AccessToken)
	outsiderID := identEnv.userID(t, outsiderSession.AccessToken)

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating ed25519 key: %v", err)
	}
	d, err := devSvc.Enroll(ctx, devices.EnrollParams{
		UserID: ownerID, Name: "Owner's Laptop", OS: "linux", ClientVersion: "0.1.0", PublicKey: pub,
	})
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}

	if _, err := devSvc.Get(ctx, outsiderID, d.ID); err != devices.ErrNotFound {
		t.Fatalf("expected ErrNotFound for outsider reading another user's device, got %v", err)
	}
	if err := devSvc.Revoke(ctx, outsiderID, d.ID); err != devices.ErrNotFound {
		t.Fatalf("expected ErrNotFound for outsider revoking another user's device, got %v", err)
	}
}

func TestDevices_OrgScopedApprovalRequiresAdminOrOwner(t *testing.T) {
	devSvc, orgSvc, identEnv := newDeviceOrgTestEnv(t)
	ctx := context.Background()

	ownerEmail := "owner-" + uniqueEmail(t)
	memberEmail := "member-" + uniqueEmail(t)
	if err := identEnv.registerVerified(ctx, ownerEmail, testPassword, "Owner"); err != nil {
		t.Fatalf("register owner: %v", err)
	}
	if err := identEnv.registerVerified(ctx, memberEmail, testPassword, "Member"); err != nil {
		t.Fatalf("register member: %v", err)
	}
	ownerSession, err := identEnv.svc.Login(ctx, identity.LoginParams{Email: ownerEmail, Password: testPassword})
	if err != nil {
		t.Fatalf("login owner: %v", err)
	}
	memberSession, err := identEnv.svc.Login(ctx, identity.LoginParams{Email: memberEmail, Password: testPassword})
	if err != nil {
		t.Fatalf("login member: %v", err)
	}
	ownerID := identEnv.userID(t, ownerSession.AccessToken)
	memberID := identEnv.userID(t, memberSession.AccessToken)

	org, err := orgSvc.Create(ctx, ownerID, "Acme Corp")
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}
	if err := orgSvc.Invite(ctx, ownerID, org.ID, memberEmail, "member"); err != nil {
		t.Fatalf("invite member: %v", err)
	}
	if _, err := orgSvc.AcceptInvitation(ctx, memberID, memberEmail, lastInviteToken(t, identEnv)); err != nil {
		t.Fatalf("accept invitation: %v", err)
	}

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating ed25519 key: %v", err)
	}
	// The plain member enrolls the device themselves — enrolling it is
	// not the gate; approving it into the org's shared storage scope is.
	d, err := devSvc.Enroll(ctx, devices.EnrollParams{
		UserID: memberID, OrganizationID: &org.ID, Name: "Org Laptop", OS: "linux", ClientVersion: "0.1.0", PublicKey: pub,
	})
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}

	// A plain member — even the device's own enrolling user — cannot
	// approve an org-scoped device on their own say-so.
	if err := devSvc.Approve(ctx, memberID, d.ID); err != devices.ErrForbidden {
		t.Fatalf("expected ErrForbidden for a plain member approving an org device, got %v", err)
	}

	// The org's owner can.
	if err := devSvc.Approve(ctx, ownerID, d.ID); err != nil {
		t.Fatalf("expected owner to be able to approve an org device, got %v", err)
	}
}

func TestDevices_WrappedVaultKey_StoredForOwnerOnly(t *testing.T) {
	devSvc, identEnv := newDeviceTestEnv(t)
	ctx := context.Background()
	mk := func(name string) (uuid.UUID, uuid.UUID) {
		email := name + uniqueEmail(t)
		if err := identEnv.registerVerified(ctx, email, testPassword, name); err != nil {
			t.Fatal(err)
		}
		s, err := identEnv.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
		if err != nil {
			t.Fatal(err)
		}
		uid := identEnv.userID(t, s.AccessToken)
		pub, _, _ := ed25519.GenerateKey(nil)
		d, err := devSvc.Enroll(ctx, devices.EnrollParams{UserID: uid, Name: name, OS: "linux", ClientVersion: "t", PublicKey: pub})
		if err != nil {
			t.Fatal(err)
		}
		return uid, d.ID
	}
	owner, deviceID := mk("owner")
	other, _ := mk("other")

	if _, err := devSvc.GetVaultKey(ctx, owner, deviceID); !errors.Is(err, devices.ErrNoVaultKey) {
		t.Fatalf("expected ErrNoVaultKey before upload, got %v", err)
	}
	if err := devSvc.SetVaultKey(ctx, owner, deviceID, []byte(`{"nope":1}`)); !errors.Is(err, devices.ErrInvalidVaultKey) {
		t.Fatalf("expected ErrInvalidVaultKey, got %v", err)
	}
	wrapped := []byte(`{"format_version":1,"salt":"aa","nonce":"bb","ciphertext":"cc"}`)
	if err := devSvc.SetVaultKey(ctx, owner, deviceID, wrapped); err != nil {
		t.Fatal(err)
	}
	got, err := devSvc.GetVaultKey(ctx, owner, deviceID)
	if err != nil || !strings.Contains(string(got), `"ciphertext"`) {
		t.Fatalf("round trip failed: %s %v", got, err)
	}
	if _, err := devSvc.GetVaultKey(ctx, other, deviceID); err == nil {
		t.Fatal("another user must not read the wrapped key")
	}
	if err := devSvc.SetVaultKey(ctx, other, deviceID, wrapped); err == nil {
		t.Fatal("another user must not overwrite the wrapped key")
	}
}
