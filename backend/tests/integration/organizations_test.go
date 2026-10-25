//go:build integration

package integration

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/internal/identity"
	"deeparchive/internal/organizations"
)

func newOrgTestEnv(t *testing.T) (*organizations.Service, *testEnv) {
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
	orgSvc := organizations.NewService(pool, identEnv.sender, logger)
	return orgSvc, identEnv
}

func TestOrganizations_CreateGrantsOwnerRole(t *testing.T) {
	orgSvc, identEnv := newOrgTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := identEnv.registerVerified(ctx, email, testPassword, "Owner"); err != nil {
		t.Fatalf("register: %v", err)
	}
	session, err := identEnv.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	userID := identEnv.userID(t, session.AccessToken)

	org, err := orgSvc.Create(ctx, userID, "Acme Corp")
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}

	_, role, err := orgSvc.Get(ctx, userID, org.ID)
	if err != nil {
		t.Fatalf("get organization: %v", err)
	}
	if role != "owner" {
		t.Fatalf("expected creator to be owner, got %q", role)
	}
}

func TestOrganizations_NonMemberCannotReadOrganization(t *testing.T) {
	orgSvc, identEnv := newOrgTestEnv(t)
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

	org, err := orgSvc.Create(ctx, ownerID, "Private Org")
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}

	if _, _, err := orgSvc.Get(ctx, outsiderID, org.ID); err != organizations.ErrNotFound {
		t.Fatalf("expected ErrNotFound for non-member read, got %v", err)
	}
	if err := orgSvc.Invite(ctx, outsiderID, org.ID, "someone@example.test", "member"); err != organizations.ErrNotFound {
		t.Fatalf("expected non-member invite attempt to fail with ErrNotFound, got %v", err)
	}
	if err := orgSvc.RemoveMember(ctx, outsiderID, org.ID, ownerID); err != organizations.ErrNotFound {
		t.Fatalf("expected non-member removal attempt to fail with ErrNotFound, got %v", err)
	}
}

func TestOrganizations_MemberCannotInviteOrManageMembers(t *testing.T) {
	orgSvc, identEnv := newOrgTestEnv(t)
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
		t.Fatalf("owner invite: %v", err)
	}
	if _, err := orgSvc.AcceptInvitation(ctx, memberID, memberEmail, lastInviteToken(t, identEnv)); err != nil {
		t.Fatalf("accept invitation: %v", err)
	}

	// A plain member cannot invite others or remove members.
	if err := orgSvc.Invite(ctx, memberID, org.ID, "third@example.test", "member"); err != organizations.ErrForbidden {
		t.Fatalf("expected member invite attempt to be forbidden, got %v", err)
	}
	if err := orgSvc.RemoveMember(ctx, memberID, org.ID, ownerID); err != organizations.ErrForbidden {
		t.Fatalf("expected member removing owner to be forbidden, got %v", err)
	}
}

func TestOrganizations_CannotRemoveOrDemoteLastOwner(t *testing.T) {
	orgSvc, identEnv := newOrgTestEnv(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if err := identEnv.registerVerified(ctx, email, testPassword, "Sole Owner"); err != nil {
		t.Fatalf("register: %v", err)
	}
	session, err := identEnv.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	userID := identEnv.userID(t, session.AccessToken)

	org, err := orgSvc.Create(ctx, userID, "Solo Org")
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}

	if err := orgSvc.UpdateMemberRole(ctx, userID, org.ID, userID, "member"); err != organizations.ErrLastOwner {
		t.Fatalf("expected ErrLastOwner demoting sole owner, got %v", err)
	}
	if err := orgSvc.RemoveMember(ctx, userID, org.ID, userID); err != organizations.ErrLastOwner {
		t.Fatalf("expected ErrLastOwner removing sole owner, got %v", err)
	}
	if err := orgSvc.Leave(ctx, userID, org.ID); err != organizations.ErrLastOwner {
		t.Fatalf("expected ErrLastOwner for sole owner leaving, got %v", err)
	}
}

func TestOrganizations_InvitationCannotBeAcceptedByWrongEmail(t *testing.T) {
	orgSvc, identEnv := newOrgTestEnv(t)
	ctx := context.Background()

	ownerEmail := "owner-" + uniqueEmail(t)
	invitedEmail := "invited-" + uniqueEmail(t)
	wrongEmail := "wrong-" + uniqueEmail(t)
	for _, e := range []string{ownerEmail, invitedEmail, wrongEmail} {
		if err := identEnv.registerVerified(ctx, e, testPassword, "User"); err != nil {
			t.Fatalf("register %s: %v", e, err)
		}
	}
	ownerSession, err := identEnv.svc.Login(ctx, identity.LoginParams{Email: ownerEmail, Password: testPassword})
	if err != nil {
		t.Fatalf("login owner: %v", err)
	}
	wrongSession, err := identEnv.svc.Login(ctx, identity.LoginParams{Email: wrongEmail, Password: testPassword})
	if err != nil {
		t.Fatalf("login wrong user: %v", err)
	}
	ownerID := identEnv.userID(t, ownerSession.AccessToken)
	wrongID := identEnv.userID(t, wrongSession.AccessToken)

	org, err := orgSvc.Create(ctx, ownerID, "Acme Corp")
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}
	if err := orgSvc.Invite(ctx, ownerID, org.ID, invitedEmail, "member"); err != nil {
		t.Fatalf("invite: %v", err)
	}
	token := lastInviteToken(t, identEnv)

	if _, err := orgSvc.AcceptInvitation(ctx, wrongID, wrongEmail, token); err != organizations.ErrEmailMismatch {
		t.Fatalf("expected ErrEmailMismatch, got %v", err)
	}
}

// lastInviteToken extracts the invitation token from the most recently
// captured email, mirroring extractToken's convention used throughout this
// package for verification/reset tokens.
func lastInviteToken(t *testing.T, env *testEnv) string {
	t.Helper()
	token := extractToken(env.sender.last().Body)
	if token == "" {
		t.Fatal("could not extract invitation token from email body")
	}
	return token
}
