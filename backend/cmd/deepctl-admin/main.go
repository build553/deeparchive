// Command deepctl-admin is the platform-operator CLI: it talks to
// Postgres (and, for retention, object storage) directly with the same
// DATABASE_URL-level trust cmd/migrate already assumes — there is no
// user-session authorization check anywhere in here, because whoever can
// run this binary already has full database access.
//
// Deliberately a separate binary from deepctl (backend/cmd/deepctl, the
// end-user CLI that talks to the HTTP API like any other client): a
// platform admin (this tool; operates on every account) and an
// organization admin (a role inside one org, reachable through deepctl's
// normal HTTP-session commands like any other member) are two different
// kinds of "admin" with two different trust boundaries, and giving them
// the same binary name invited confusing the two.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"deeparchive/internal/backups"
	"deeparchive/internal/config"
	"deeparchive/internal/identity"
	"deeparchive/internal/notifications"
	"deeparchive/internal/observability"
	"deeparchive/internal/organizations"
	"deeparchive/internal/quotas"
	"deeparchive/internal/retention"
	"deeparchive/internal/storage"
	"deeparchive/internal/usage"
)

// version is overwritten at build time via -ldflags "-X main.version=...".
// See .github/workflows/release.yml.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type deps struct {
	identity  *identity.Service
	orgs      *organizations.Service
	usage     *usage.Service
	retention *retention.Service
}

func run() error {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help" {
		printUsage()
		if len(os.Args) < 2 {
			os.Exit(2)
		}
		return nil
	}
	if os.Args[1] == "version" || os.Args[1] == "-v" || os.Args[1] == "--version" {
		fmt.Println("deepctl-admin", version)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
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
	sender := notifications.DevLogSender{Logger: logger}
	accessTokens := identity.NewAccessTokenIssuer(cfg.Auth.AccessTokenSigningKey, cfg.Auth.AccessTokenTTL)
	identitySvc := identity.NewService(pool, sender, accessTokens, cfg.Auth.RefreshTokenTTL, logger)
	orgsSvc := organizations.NewService(pool, sender, logger)

	storageClient, err := storage.NewClient(cfg.S3)
	if err != nil {
		return fmt.Errorf("creating object storage client: %w", err)
	}
	usageSvc := usage.NewService(pool)
	quotasSvc := quotas.NewService(pool, usageSvc)
	backupsSvc := backups.NewService(pool, storageClient, quotasSvc, logger)
	retentionSvc := retention.NewService(pool, backupsSvc, logger)

	d := &deps{identity: identitySvc, orgs: orgsSvc, usage: usageSvc, retention: retentionSvc}

	switch os.Args[1] {
	case "users":
		return runUsers(ctx, d, os.Args[2:])
	case "orgs":
		return runOrgs(ctx, d, os.Args[2:])
	case "retention":
		return runRetention(ctx, d, os.Args[2:])
	case "usage":
		return runUsage(ctx, d, os.Args[2:])
	default:
		printUsage()
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `deepctl-admin — DeepArchive platform-operator CLI

For platform admins only — operates on every account via direct database
access. Not for day-to-day account use; see deepctl for that (device
enrollment, backup, restore, login).

Usage:
  deepctl-admin users list [--limit N] [--offset N]
  deepctl-admin users show <email>
  deepctl-admin users set-quota <email> <bytes>
  deepctl-admin users set-retention <email> <keep>
  deepctl-admin users suspend <email>
  deepctl-admin users activate <email>
  deepctl-admin users deactivate <email>
  deepctl-admin users set-role <email> <none|operator|admin>

  deepctl-admin orgs list [--limit N] [--offset N]
  deepctl-admin orgs show <org-id>
  deepctl-admin orgs set-quota <org-id> <bytes>
  deepctl-admin orgs set-retention <org-id> <keep>

  deepctl-admin retention sweep

  deepctl-admin usage <email>

  deepctl-admin version

Reads configuration from the environment exactly like the api/worker
processes (DATABASE_URL, S3_*, ACCESS_TOKEN_SIGNING_KEY, ...) — run it
with the same .env the rest of the stack uses.
`)
}

func parseInt64Arg(name, s string) (int64, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return v, nil
}

func parseInt32Arg(name, s string) (int32, error) {
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return int32(v), nil
}

// parsePageFlags scans a very small, fixed set of --limit/--offset flags
// out of args. Not a general flag parser (deepctl-admin's commands don't need
// one) — just enough for list subcommands' optional pagination.
func parsePageFlags(args []string) (limit, offset int32, rest []string) {
	limit, offset = 50, 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit":
			if i+1 < len(args) {
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					limit = int32(v)
				}
				i++
				continue
			}
		case "--offset":
			if i+1 < len(args) {
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					offset = int32(v)
				}
				i++
				continue
			}
		}
		rest = append(rest, args[i])
	}
	return limit, offset, rest
}
