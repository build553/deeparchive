# DeepArchive

DeepArchive is an encrypted cloud backup platform: desktop clients encrypt
files locally and upload them directly to S3-compatible object storage;
a Go backend owns identity, authorization, job/quota/retention state, and
issues short-lived upload/download permissions; a web dashboard gives
remote visibility into backup and snapshot history.

**Status: early development.** See `docs/implementation-status.md` for an
honest, continuously-updated account of what is implemented and verified
versus still planned. Do not assume a feature exists because it is
described in this README's target architecture below — check that file.

## Architecture

- **Backend** (`backend/`): Go, Chi router, PostgreSQL via pgx/sqlc,
  golang-migrate migrations. Three processes from one codebase: `cmd/api`
  (HTTP API), `cmd/worker` (background jobs — currently storage quota +
  snapshot retention enforcement, on a configurable interval), and
  two CLIs, `cmd/deepctl` and `cmd/deepctl-admin` (see below).
- **Account lifecycle** ([docs](docs/account-lifecycle.md)): 15-minute email verification, 15-day deactivation recovery, terms/age consent.
- **End-user CLI** (`backend/cmd/deepctl`, [docs](docs/deepctl.md)): pure
  Go. `login` (browser device-code flow, like `gh auth login`), `init`,
  `backup`, `list`, `restore`. Talks only to the HTTP API.
- **Operator CLI** (`backend/cmd/deepctl-admin`,
  [docs](docs/deepctl-admin.md)): list/inspect users and organizations,
  set storage quotas and retention limits,
  suspend/activate/deactivate accounts, set platform roles, trigger a
  retention sweep on demand. Talks to Postgres directly with the same
  trust level `cmd/migrate` has — platform operators only, a separate
  binary on purpose. Both CLIs are released as native binaries for
  linux/macOS/Windows via `.github/workflows/release.yml` (push a
  `v*.*.*` tag).
- **Backup engine** (`crates/backup-core/`, `crates/backup-cli/`): Rust.
  Filesystem scanning, content-defined chunking, client-side encryption,
  and restore are implemented and verified end-to-end (Milestone 3,
  `make test-e2e`). Not yet implemented: incremental backups, upload
  retry/resume, and multi-device vault sharing — see
  `docs/backup-protocol.md` and `docs/encryption.md`.
- **Desktop app** (`apps/desktop/`): Tauri 2 + React + TypeScript. Not yet
  scaffolded (Milestone 5).
- **Web dashboard** (`apps/web/`): React + TypeScript + Vite. Login
  (incl. MFA), account settings, security (password/MFA/sessions),
  device management (list/approve/revoke — enrollment stays a
  `deepctl` operation), backup/snapshot history (list/delete),
  organizations (create/invite/roles), and storage usage are all
  implemented (Milestone 6). Not implemented: in-browser
  restore/decryption (needs the vault passphrase + client-side crypto —
  restoring stays a `deepctl` operation).
- **Infra** (`infra/`, `compose.yaml`): Docker Compose for local dev —
  Postgres, MinIO (S3-compatible), the API, the worker, and the web
  dashboard.
- **Dev Inbox**: when `APP_ENV=development` (set it explicitly; unset means `production`), the API
  captures every "sent" email in memory and exposes it, unauthenticated,
  at `/dev/inbox` in the web dashboard — the only way to read a
  verification/password-reset/invitation token locally, since no real
  email provider is wired up yet. Two independent gates keep it from
  existing outside development — see
  `docs/implementation-status.md`.

Full architecture: `docs/architecture.md`. Encryption design:
`docs/encryption.md`. Backup/restore protocol:
`docs/backup-protocol.md` (both written alongside Milestone 3). No
separate threat-model document exists yet — the identity/authorization
threat model is covered inline in `docs/implementation-status.md`'s
Milestone 2 section instead.

## Prerequisites

- Go 1.26+
- Rust stable (for `crates/backup-core`, `crates/backup-cli`)
- Node.js 22+ and pnpm (for `apps/web`)
- Docker + Docker Compose v2
- (Later milestones) Tauri's platform prerequisites, for `apps/desktop`

## Local development

```bash
cp .env.example .env
docker compose up -d --build
curl http://localhost:8080/healthz   # {"status":"ok"}
curl http://localhost:8080/readyz    # {"status":"ok","checks":{"database":"ok"}}
make seed                             # dummy accounts + an org for local testing
open http://localhost:5173            # web dashboard (production build via nginx)
```

**New to this repo and want to actually click through it?** See
`docs/local-dev-walkthrough.md` — seed data, the Dev Inbox (so
verification/password-reset/invitation emails are readable without a
real provider — see below), and a full account/backup/org workflow.

For frontend development with hot reload instead of rebuilding the
`web` container on every change, stop that container and run
`make dev-web` (Vite's dev server) against the backend started above.

Backend-only workflow without Docker for the API process itself (Postgres
and MinIO still need to be running, e.g. via `docker compose up -d postgres
minio minio-init`):

```bash
cd backend
go run ./cmd/migrate up
go run ./cmd/api
```

Stop everything and delete local dev data (destructive — see `make clean`):

```bash
docker compose down -v
```

## Commands

```bash
make dev                # docker compose up --build (backend + worker + web)
make dev-web             # web dashboard's Vite dev server only, with hot reload
make build               # go build ./... AND pnpm --filter web build
make test                # unit tests only; prints what it did NOT run and why
make test-unit           # go test -race -short ./...
make test-integration    # brings up postgres+minio, runs tests/integration
make test-e2e            # backup/restore round trip against the live stack (deepctl)
make lint                # gofmt + go vet (golangci-lint if installed) + eslint for apps/web
make typecheck           # tsc for apps/web (apps/desktop has no code yet)
make migrate-up / migrate-down
make seed                # not implemented yet; fails loudly
make clean               # docker compose down -v — deletes local dev data
```

`make test` does not run integration or e2e tests; it says so explicitly
rather than silently skipping them. Run `make test-integration` and
`make test-e2e` separately once their prerequisites (Docker; a running
full stack) are met.

## Known limitations (current)

- `apps/desktop` is still directory scaffolding only — Milestone 5.
  `apps/web` has no in-browser restore (needs the vault passphrase +
  client-side crypto, which stays a `deepctl` operation) — see
  `docs/implementation-status.md`'s Milestone 6 section.
- `backend/api/openapi.yaml` now documents every route (fixed — it used
  to cover only `/status`). The web dashboard's TypeScript types are
  still hand-written rather than generated from it; that's a reasonable
  follow-up, not done yet.
- `deepctl-admin`'s release workflow has not been exercised on real GitHub
  Actions (no `origin` remote configured here) — only verified by
  running each target's `go build` locally.
- Backups have no incremental re-chunking, no multi-device vault sharing,
  and no upload retry/resume — see `docs/backup-protocol.md` and
  `docs/encryption.md`.
- `DELETE /api/v1/me` deactivates an account (blocks login, revokes
  sessions) rather than erasing its data — there is no hard-delete /
  right-to-erasure flow yet.
- Still no real email provider (`notifications.DevLogSender` only), and
  rate limiting is in-process memory only (not correct across multiple
  API replicas).
- CI (`.github/workflows/ci.yaml`) is written but has not been run against
  a real GitHub Actions environment from this repository yet (no `origin`
  remote is configured here).

See `docs/implementation-status.md` for the full, current picture.
