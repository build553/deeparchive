// Command seed creates dummy accounts and an organization for local
// development testing — see docs/local-dev-walkthrough.md. It refuses to
// run against a production configuration (hard check, not a suggestion):
// this tool creates real rows with a publicly-known password, which must
// never happen outside a disposable local database.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/db/generated"
	"deeparchive/internal/config"
	"deeparchive/internal/dbtypes"
	"deeparchive/internal/identity"
	"deeparchive/internal/notifications"
	"deeparchive/internal/observability"
	"deeparchive/internal/organizations"
)

// devPassword is deliberately public — this is seed data for a local
// disposable database, not a secret.
const devPassword = "DevPassword123!"

type seedAccount struct {
	email       string
	displayName string
	orgRole     string // "owner", "admin", or "member"
}

var accounts = []seedAccount{
	{email: "alice@example.test", displayName: "Alice (owner)", orgRole: "owner"},
	{email: "bob@example.test", displayName: "Bob (admin)", orgRole: "admin"},
	{email: "carol@example.test", displayName: "Carol (member)", orgRole: "member"},
}

const orgName = "Acme Dev Org"

// adminEmail is a platform super-admin — platform_role = "admin", not a
// member of any organization — so the web admin panel
// (docs/local-dev-walkthrough.md) has a standing account to log in as
// without needing deepctl/direct DB access.
const adminEmail = "admin@example.test"
const adminDisplayName = "Admin (platform)"

type createdAccount struct {
	account seedAccount
	userID  uuid.UUID
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg.Env == "production" {
		return fmt.Errorf("refusing to seed dummy accounts into a production environment")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DB.URL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("database not reachable: %w", err)
	}

	logger := observability.NewLogger(cfg.Env)
	// A plain log-only sender: this process's own "sent" emails (the
	// verification email Register triggers) are irrelevant here, since
	// we immediately mark the account verified directly below rather
	// than making a human read that email — see
	// docs/local-dev-walkthrough.md for the flow that DOES want a human
	// to read a captured email (registering a *new*, non-seeded account).
	sender := notifications.DevLogSender{Logger: logger}
	accessTokens := identity.NewAccessTokenIssuer(cfg.Auth.AccessTokenSigningKey, cfg.Auth.AccessTokenTTL)
	identitySvc := identity.NewService(pool, sender, accessTokens, cfg.Auth.RefreshTokenTTL, logger)
	orgsSvc := organizations.NewService(pool, sender, logger)
	queries := generated.New(pool)

	createdAccounts, err := createAccounts(ctx, identitySvc, queries)
	if err != nil {
		return err
	}

	orgID, err := ensureOrganization(ctx, orgsSvc, createdAccounts[0])
	if err != nil {
		return err
	}

	if err := addMembers(ctx, queries, orgID, createdAccounts[1:]); err != nil {
		return err
	}

	if err := ensurePlatformAdmin(ctx, identitySvc, queries); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Seed accounts (password for all):", devPassword)
	fmt.Println()
	for _, c := range createdAccounts {
		fmt.Printf("  %-25s %-6s in %s\n", c.account.email, c.account.orgRole, orgName)
	}
	fmt.Printf("  %-25s %-6s (platform super-admin, no org)\n", adminEmail, "admin")
	return nil
}

// ensurePlatformAdmin creates (or promotes, if it already exists from an
// earlier run that predates this role) the standing super-admin account
// the web admin panel is meant to be logged into.
func ensurePlatformAdmin(ctx context.Context, identitySvc *identity.Service, queries *generated.Queries) error {
	existing, err := identitySvc.AdminGetUserByEmail(ctx, adminEmail)
	if err != nil && !errors.Is(err, identity.ErrNotFound) {
		return fmt.Errorf("looking up %s: %w", adminEmail, err)
	}
	var userID uuid.UUID
	if err == nil {
		userID = existing.ID
	} else {
		if err := identitySvc.Register(ctx, adminEmail, devPassword, adminDisplayName); err != nil {
			return fmt.Errorf("registering %s: %w", adminEmail, err)
		}
		u, err := identitySvc.AdminGetUserByEmail(ctx, adminEmail)
		if err != nil {
			return fmt.Errorf("looking up newly created %s: %w", adminEmail, err)
		}
		userID = u.ID
		if err := queries.MarkEmailVerified(ctx, dbtypes.UUID(userID)); err != nil {
			return fmt.Errorf("verifying %s: %w", adminEmail, err)
		}
		fmt.Printf("%-25s created\n", adminEmail)
	}
	if err := identitySvc.AdminSetPlatformRole(ctx, userID, userID, "admin"); err != nil {
		return fmt.Errorf("promoting %s to platform admin: %w", adminEmail, err)
	}
	return nil
}

func createAccounts(ctx context.Context, identitySvc *identity.Service, queries *generated.Queries) ([]createdAccount, error) {
	out := make([]createdAccount, 0, len(accounts))
	for _, a := range accounts {
		existing, err := identitySvc.AdminGetUserByEmail(ctx, a.email)
		if err == nil {
			fmt.Printf("%-25s already exists, skipping\n", a.email)
			out = append(out, createdAccount{account: a, userID: existing.ID})
			continue
		}
		if !errors.Is(err, identity.ErrNotFound) {
			return nil, fmt.Errorf("looking up %s: %w", a.email, err)
		}

		if err := identitySvc.Register(ctx, a.email, devPassword, a.displayName); err != nil {
			return nil, fmt.Errorf("registering %s: %w", a.email, err)
		}
		u, err := identitySvc.AdminGetUserByEmail(ctx, a.email)
		if err != nil {
			return nil, fmt.Errorf("looking up newly created %s: %w", a.email, err)
		}
		// Bypass the verification-email ceremony on purpose — seed data
		// should be immediately usable. docs/local-dev-walkthrough.md
		// exercises the real token-via-email flow for a fresh account
		// instead of this one.
		if err := queries.MarkEmailVerified(ctx, dbtypes.UUID(u.ID)); err != nil {
			return nil, fmt.Errorf("verifying %s: %w", a.email, err)
		}
		fmt.Printf("%-25s created\n", a.email)
		out = append(out, createdAccount{account: a, userID: u.ID})
	}
	return out, nil
}

func ensureOrganization(ctx context.Context, orgsSvc *organizations.Service, owner createdAccount) (uuid.UUID, error) {
	existingOrgs, err := orgsSvc.ListForUser(ctx, owner.userID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("listing organizations for %s: %w", owner.account.email, err)
	}
	for _, o := range existingOrgs {
		if o.Name == orgName {
			fmt.Printf("%-25s already exists, skipping\n", orgName)
			return o.ID, nil
		}
	}
	org, err := orgsSvc.Create(ctx, owner.userID, orgName)
	if err != nil {
		return uuid.Nil, fmt.Errorf("creating organization: %w", err)
	}
	fmt.Printf("%-25s created (owner: %s)\n", orgName, owner.account.email)
	return org.ID, nil
}

func addMembers(ctx context.Context, queries *generated.Queries, orgID uuid.UUID, members []createdAccount) error {
	for _, c := range members {
		_, err := queries.GetMembership(ctx, generated.GetMembershipParams{
			OrganizationID: dbtypes.UUID(orgID), UserID: dbtypes.UUID(c.userID),
		})
		if err == nil {
			continue // already a member
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("checking membership for %s: %w", c.account.email, err)
		}
		if _, err := queries.CreateMembership(ctx, generated.CreateMembershipParams{
			OrganizationID: dbtypes.UUID(orgID),
			UserID:         dbtypes.UUID(c.userID),
			Role:           c.account.orgRole,
		}); err != nil {
			return fmt.Errorf("adding %s to %s: %w", c.account.email, orgName, err)
		}
		fmt.Printf("  added %s as %s\n", c.account.email, c.account.orgRole)
	}
	return nil
}
