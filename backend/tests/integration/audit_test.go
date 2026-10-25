//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/internal/identity"
)

// TestAuditEvents_WrittenForSecuritySensitiveActions verifies that audit
// rows actually land in the database for the actions Milestone 2's
// acceptance criteria call out (login, logout, password change) — not
// just that the identity.Service methods succeed. A previous version of
// this package had a stale doc comment claiming audit events were written
// for registration duplicates with no code behind it; this test exists so
// that kind of drift is caught by CI, not by a doc comment nobody
// re-reads.
func TestAuditEvents_WrittenForSecuritySensitiveActions(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connecting to database: %v", err)
	}
	defer pool.Close()

	email := uniqueEmail(t)
	if err := env.registerVerified(ctx, email, testPassword, "Audit Test"); err != nil {
		t.Fatalf("register: %v", err)
	}
	session, err := env.svc.Login(ctx, identity.LoginParams{Email: email, Password: testPassword, RemoteAddr: "127.0.0.1"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	userID := env.userID(t, session.AccessToken)
	if err := env.svc.Logout(ctx, userID, session.SessionID); err != nil {
		t.Fatalf("logout: %v", err)
	}

	var count int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE actor_user_id = $1 AND event_type = ANY($2)`,
		userID, []string{"auth.register", "auth.login", "auth.logout"},
	).Scan(&count)
	if err != nil {
		t.Fatalf("querying audit_events: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 audit events (register, login, logout), got %d", count)
	}
}
