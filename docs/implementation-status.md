# Implementation status

This document is the source of truth for what is actually built and tested,
versus planned. It is updated at the end of every work session. Claims here
must be verifiable by running the commands listed.

Last updated: 2026-10-23 (Milestone 6 complete + deepctl-admin operator CLI
and cross-platform release pipeline + openapi.yaml fixed + Dev Inbox,
seed data, and local dev walkthrough).

## Milestone 1 — Foundation: DONE

What exists and is verified working:

- Monorepo layout under `apps/`, `backend/`, `crates/`, `packages/`,
  `infra/`, `docs/` per the target structure. Directories for later
  milestones (apps/desktop, apps/web, crates/backup-core, packages/*) exist
  as empty scaffolding only where the structure requires them; no fake
  implementations were placed inside them.
- Go module `deeparchive` (`backend/go.mod`), Go 1.23, builds cleanly
  (`go build ./...`), passes `go vet ./...` and `gofmt -l .` cleanly.
- `backend/internal/config`: environment-based configuration with real
  validation (missing `DATABASE_URL`, missing S3 credentials, and
  production without an explicit signing key / CORS allowlist all fail
  fast with descriptive errors instead of silently using insecure
  defaults). Unit-tested in `config_test.go`.
- `backend/internal/observability`: structured JSON logging via `log/slog`,
  with a redaction list for sensitive field names (defense in depth; the
  real control is simply never passing secrets as log attributes).
- `backend/internal/middleware`: request ID propagation, panic recovery,
  access logging, request body size limits.
- `backend/internal/health`: `/healthz` (liveness, zero dependencies) and
  `/readyz` (checks a real `pgxpool.Pool.Ping` against Postgres).
  Unit-tested with a fake failing pinger to confirm `/readyz` returns 503
  when the database is down.
- `backend/internal/httpserver`: chi router, CORS allowlist from config,
  conservative security headers, `/api/v1/status`.
- `backend/cmd/api`, `backend/cmd/worker`, `backend/cmd/migrate`: real
  entrypoints. `api` opens a pgx pool, pings the DB at startup (fails fast
  if unreachable), serves HTTP with graceful shutdown on SIGINT/SIGTERM.
  `worker` has the same startup/shutdown skeleton but no jobs yet
  (Milestone 4) — it stays alive rather than exiting, which would
  crash-loop under a container orchestrator.
- `backend/db/migrations/0001_init_extensions`: enables `pgcrypto`.
  Applied successfully via `migrate` against a real Postgres 17 container.
- `backend/api/openapi.yaml`: OpenAPI 3.1 skeleton, currently documenting
  only `/status` (the only implemented business route so far). Grows with
  each route that ships.
- `compose.yaml` + `infra/docker/{api,worker,migrate}.Dockerfile`: real
  multi-stage builds, non-root runtime user, Postgres 17 + MinIO +
  one-shot `migrate` + `minio-init` (bucket creation) + `api` + `worker`.
  **Verified by actually running `docker compose up -d --build`:**
  - `migrate` container exited 0, applied migration version 1.
  - `postgres` and `minio` containers report healthy.
  - `api` container reports healthy; `GET /healthz` → `200 {"status":"ok"}`;
    `GET /readyz` → `200 {"status":"ok","checks":{"database":"ok"}}`;
    `GET /api/v1/status` → 200 with service/env/version JSON.
  - `worker` container started and stayed up (no crash loop).
- `sqlc.yaml` configured (schema=`db/migrations`, queries=`db/queries`,
  pgx/v5 codegen); no queries written yet since there is no schema beyond
  extensions — this lands with Milestone 2's identity tables.
- `golang-migrate` and `sqlc` CLIs installed locally (`$(go env GOPATH)/bin`)
  and used for everything above; not yet added as pinned devDependency
  versions in a tool-version file (tracked as a follow-up, not a blocker).
- `Makefile`: `dev`, `build`, `test` (unit only, and says so),
  `test-unit`, `test-integration` (brings up postgres+minio, requires
  Docker), `test-e2e` (explicitly fails with a message — not implemented
  yet, Milestone 6/Playwright), `lint`, `typecheck` (explicitly says no
  frontend exists yet), `migrate-up`, `migrate-down`, `seed` (explicitly
  fails — not implemented), `clean` (documented as destructive, deletes
  local volumes).
- `.github/workflows/ci.yaml`: gofmt/vet/build/unit-test job, a
  Postgres-service integration job (currently a no-op since no
  `tests/integration/*.go` files exist yet), and OpenAPI spec validation.
  **Not yet executed in a real GitHub Actions run** — no `origin` remote
  configured in this environment. This is a known gap: CI is written but
  unverified until pushed.
- Repo initialized with `git init`; no commits made yet (per the standing
  instruction to only commit when explicitly asked).

### Explicitly NOT done in Milestone 1 (do not assume otherwise)

- No identity/auth tables, no users, no sessions. `internal/identity` etc.
  are empty directories — Milestone 2.
- `apps/web` and `apps/desktop` have only the directory skeleton from the
  spec; there is no `package.json`, no React code, nothing to build or
  run. `infra/docker/web.Dockerfile` exists but will fail to build until
  Milestone 6 scaffolds an actual Vite app — it is intentionally left out
  of `compose.yaml` for that reason.
- `crates/backup-core` is an empty directory skeleton; no `Cargo.toml`,
  no Rust code yet — Milestone 3.
- `packages/*` (api-client, shared-types, ui, config) are empty — created
  alongside the frontends that consume them.
- No dependency/vulnerability scanning wired into CI yet.
- No golangci-lint config; `make lint` falls back to `go vet` if
  golangci-lint isn't installed, which is a materially weaker check and
  should be tightened before Milestone 7.

## Commands that actually work right now

```
cp .env.example .env
docker compose up -d --build       # postgres, minio, migrate (one-shot), api, worker
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
curl http://localhost:8080/api/v1/status
docker compose down -v             # stop and delete local dev data

cd backend
go build ./...
go vet ./...
gofmt -l .                         # must print nothing
go test -race ./...
go run ./cmd/migrate up            # against a local DATABASE_URL (see .env.example)
```

## Milestone 2 — Identity and authorization: DONE

Acceptance criteria check: unauthorized access fails (every protected
route 401s without a valid, live, non-revoked token); cross-tenant access
fails (organization and device tenant-isolation tests above); session
revocation works and takes effect immediately, not just at JWT expiry
(verified for both user sessions and device tokens); no privileged
operation is authorized solely by a client-supplied ID (every service
method re-derives ownership/role from the database using the
authenticated actor's ID, never a request body/path field). All of this
is backed by integration tests against a real database plus manual
verification against the real running containerized API — not just unit
tests against mocks.

### Done and verified

- `backend/db/migrations/0002_identity`: users, organizations,
  memberships, invitations, sessions (with refresh-token rotation +
  short-grace-window replay detection via
  `previous_refresh_token_hash`/`previous_token_expires_at`),
  email_verification_tokens, password_reset_tokens, mfa_credentials,
  mfa_recovery_codes, audit_events. Applied and rolled back cleanly
  against the real Postgres container (`migrate up` / `migrate down 2` /
  `migrate up` all verified).
- sqlc queries + generated code for all of the above
  (`backend/db/queries/*.sql`, `backend/db/generated/*.go`).
- `backend/internal/identity`: real implementation, not a stub —
  - Argon2id password hashing (`password.go`), self-describing encoded
    hash format, unit-tested (hash/verify round-trip, wrong password
    rejected, distinct salts, policy enforcement).
  - Opaque bearer tokens for sessions/verification/reset (SHA-256 hashed
    at rest, never stored in plaintext).
  - JWT (HS256, `golang-jwt/jwt/v5`) short-lived access tokens.
  - `Register`/`ResendVerification`/`VerifyEmail`: enumeration-resistant
    (duplicate registration and unknown-email resend both return success
    without sending a second email / any email, respectively).
  - `Login`: identical error for unknown email vs. wrong password
    (`ErrInvalidCredentials`, with a decoy Argon2id verification on the
    unknown-email path to reduce timing-based enumeration); account
    lockout after repeated failures; MFA-required second factor.
  - Session issuance, refresh-token rotation, and **replay detection**:
    presenting an already-rotated-away refresh token within its grace
    window revokes the entire session family. Verified by an integration
    test that rotates, confirms the new token works, then replays the old
    one and confirms *both* the old and the rotated token are dead
    afterward.
  - Logout / logout-all / revoke-one-session (ownership-checked — a user
    cannot revoke another user's session by guessing its ID,
    integration-tested) / revoke-others.
  - **Live session revocation, not just stateless JWT expiry**: the auth
    middleware (`middleware.go`) re-checks the session row in Postgres on
    every authenticated request. Verified against the real running API
    container: logging out immediately invalidates a still-unexpired
    access token (confirmed via `curl` — `/me` returns 401 right after
    `/auth/logout`, well before the token's 15-minute `exp`).
  - Password change (requires current password, revokes all other
    sessions) and password reset (single-use token, revokes *all*
    sessions) — both integration-tested against the real database.
  - MFA: TOTP enroll → confirm (requires a live valid code before
    enabling) → login requiring the code → single-use recovery codes.
    TOTP secrets are AES-256-GCM encrypted at rest (key derived from
    `ACCESS_TOKEN_SIGNING_KEY` — see the explicit limitation below).
    Integration-tested end to end including reused-recovery-code
    rejection.
- HTTP layer (`handlers.go`, mounted in `httpserver/router.go`): all of
  `/api/v1/auth/*` except logout-all's organization-scoped variants (N/A
  — no orgs touch sessions yet) and `/api/v1/me` (GET only so far).
  Auth-sensitive routes (register, login, refresh, password
  forgot/reset, resend-verification) are behind a per-IP rate limiter.
  **Verified against the real containerized API** with `curl`: register
  → login → authenticated `/me` → list sessions → logout → confirmed
  both the access token and the refresh token are dead afterward; wrong
  password returns a generic 401; missing bearer token returns 401.
- 9 integration tests (`backend/tests/integration/identity_test.go`,
  build tag `integration`) run against a real disposable-per-test-data
  Postgres database — not mocks — covering every flow above. All passing.
  Run: `DATABASE_URL=... go test -tags=integration -race ./tests/integration/...`

### Organizations and memberships: DONE and verified

- `backend/internal/organizations`: `Create` (transactional —
  organization + owner membership row created atomically, so an org can
  never exist without an owner), `ListForUser`, `Get` (membership-gated;
  returns `ErrNotFound`, not `ErrForbidden`, for non-members so a probe
  can't distinguish "exists but you're not in it" from "doesn't exist"),
  `UpdateName` (owner/admin only), `ListMembers`, `Invite` (owner/admin
  only, role restricted to admin/member — never owner, since there's no
  ownership-transfer flow), `AcceptInvitation` (email on the invitation
  must case-insensitively match the authenticated account's email),
  `UpdateMemberRole` and `RemoveMember` (owner/admin only, with a
  **last-owner safeguard** — `ErrLastOwner` — blocking removal/demotion
  of an organization's sole owner), `Leave` (same last-owner safeguard).
  Every mutating action writes an audit event.
- HTTP routes mounted under `/api/v1/organizations` and
  `/api/v1/invitations/accept`, all behind the same session-aware
  `Authenticate` middleware identity uses (shared, not reimplemented).
- 5 integration tests against the real database covering: creator
  becomes owner; a non-member gets `ErrNotFound` on read/invite/remove
  (tenant isolation); a plain member is forbidden from inviting or
  removing the owner; the sole owner cannot be demoted, removed, or leave
  (all three checked); an invitation cannot be accepted by a
  different-email account. All passing.
- Verified against the real containerized API via `curl`: create org →
  get → list → members → rename → attempt to leave as sole owner
  (correctly rejected with 409).
- Known limitation: **no ownership-transfer flow.** A sole owner who
  wants to leave must first promote another member to owner via
  `UpdateMemberRole`, then leave — there is no one-step "transfer
  ownership and leave" operation. Documented in `Leave`'s doc comment,
  not hidden.
- `DELETE /api/v1/organizations/{id}` (org deletion/deactivation) is not
  implemented yet — out of scope until retention/billing semantics for
  org deletion are designed (Milestone 4 territory).

### Devices: DONE and verified

- `backend/db/migrations/0003_devices`: `devices` (owner + optional
  organization scope, status pending/active/revoked, Ed25519 public key)
  and `device_challenges` (short-lived single-use nonces).
- `backend/internal/devices`: real public-key challenge/response, not a
  placeholder —
  - `Enroll` (authenticated user, validates the public key is exactly 32
    bytes), starts in `pending` status.
  - `Approve` (owner, or an org admin/owner for org-scoped devices).
  - `IssueChallenge`: generates a random 32-byte nonce, stored
    server-side with a 2-minute expiry. **Deliberately unauthenticated**
    by user session — the device has no session yet, which is the
    problem this flow solves.
  - `Authenticate`: verifies an **Ed25519 signature** (`crypto/ed25519`,
    stdlib — no custom scheme) over the challenge nonce against the
    device's registered public key. Rejects wrong keys, expired
    challenges, and replay of an already-used challenge. On success,
    issues a **device-scoped** JWT (`devices.DeviceClaims`, distinct from
    `identity.AccessClaims` — a user session token and a device token are
    not interchangeable, and the device middleware explicitly rejects a
    token with a nil `DeviceID`, which is what you'd get from feeding it
    a differently-shaped token).
  - `Heartbeat` behind `authenticateDevice` middleware, which — like
    identity's session check — re-verifies the device's live status in
    Postgres on every call rather than trusting the stateless JWT, so
    revocation takes effect immediately.
  - `Revoke`, `Rename`, `Get`, `ListForUser`: ownership/org-membership
    gated, `ErrNotFound` (not `ErrForbidden`) for non-owners to avoid
    confirming a device ID exists.
  - Every mutating action writes an audit event.
- HTTP routes mounted under `/api/v1/devices`.
- 4 integration tests against the real database: full enroll → approve →
  challenge → **real Ed25519 sign/verify** → authenticate flow; wrong
  private key correctly rejected (`ErrInvalidSignature`); revoked device
  cannot get a new challenge; a different user cannot read or revoke
  someone else's device (`ErrNotFound`). All passing.
- **Verified against the real containerized API** end-to-end, including
  actual Ed25519 key generation and signing (a small Go client program,
  since this needed real crypto, not something curl can do): register →
  login → enroll (pending) → approve → issue challenge → sign nonce →
  authenticate → device token issued → heartbeat succeeds with the
  device token → revoke via the user's session → heartbeat with the
  *same still-unexpired device token* now returns 401
  `"device has been revoked"`. This confirms live revocation works for
  device tokens exactly like it does for user sessions.
- **Org-scoped device approval requires owner/admin role** (fixed in the
  hardening pass after Milestone 4 — previously any member could approve
  any pending device in their organization, including the device's own
  enrolling user if they only held the member role). Personal
  (non-org) devices keep simple self-approval — there's no admin concept
  for a device with no organization. Verified by
  `TestDevices_OrgScopedApprovalRequiresAdminOrOwner` against a real
  database: a plain member's approval attempt is `ErrForbidden`; the
  org's owner succeeds.

### Explicit limitations / not yet done (do not assume otherwise)

- **MFA secret encryption key is derived from `ACCESS_TOKEN_SIGNING_KEY`**
  (`mfa.go`), not an independently rotatable key or a real KMS. Rotating
  the signing key would make existing encrypted MFA secrets
  undecryptable. Acceptable for current development stage; must be
  replaced with a dedicated key (or KMS-backed envelope encryption)
  before this is production-ready. Flagged here and in `mfa.go`'s doc
  comment so it isn't missed.
- **No real email provider.** `notifications.DevLogSender` logs that an
  email *would* be sent (subject + recipient only, never the token/body)
  and returns success unconditionally. Verification links and password
  reset links are not actually delivered anywhere outside of
  integration-test capture. This blocks real-world usability of
  register/verify/forgot-password until a provider (SES, Postmark, etc.)
  is wired in — tracked, not silently ignored.
- **Rate limiting is in-process memory only**
  (`internal/middleware/ratelimit.go`), not correct across multiple API
  replicas. Fine for one instance / local dev; needs a shared store
  (Redis) before horizontal scaling.
- **`PATCH /api/v1/me` and `DELETE /api/v1/me` are now implemented**
  (fixed in the hardening pass after Milestone 4). `PATCH` updates
  `display_name` only — email has no change-with-reverification flow,
  out of scope. `DELETE` is a **soft delete**: it sets
  `status='deactivated'` (a value the `users` table's CHECK constraint
  already allowed but nothing previously set) and revokes every session,
  rather than issuing a SQL `DELETE` on the row — seeing the real
  cross-table cascade-ordering hazard that a hard delete would hit (see
  Milestone 3's note below) is exactly what motivated choosing soft
  delete instead of fixing that ordering for a feature nothing needed
  yet. A deactivated account cannot log in
  (`ErrAccountDeactivated`, mirroring the existing `ErrAccountSuspended`
  check) but its data is not destroyed. **Verified against the real
  containerized API**: `PATCH` changes the name reflected in a
  subsequent `GET /me`; `DELETE` with the wrong password 401s and
  changes nothing; `DELETE` with the correct password succeeds, the
  *same still-unexpired access token* then 401s on `GET /me`, and
  logging in again returns `account_deactivated`. Also covered by two
  integration tests against a real database.
- Devices (enrollment, challenge/response, revocation): schema not
  written yet. `internal/devices` is empty. This is the next task.
- `internal/memberships` is still an empty directory — membership logic
  lives inside `internal/organizations` instead (one cohesive service),
  which seemed cleaner than splitting it across two packages. If this
  turns out to need its own module boundary later (e.g. once billing or
  SSO cares about membership independent of org metadata), it can be
  split out then.
- Audit logging now writes real rows for every security-sensitive
  identity action (register, login success/failure, logout, logout-all,
  session revoke, password change/reset, MFA enable/disable) and every
  organization mutation (create, rename, invite, accept, role change,
  remove, leave) — verified by a dedicated integration test that queries
  `audit_events` directly rather than trusting the service call
  succeeded. Not yet covered: device actions (not built yet) and backup/
  snapshot/restore actions (Milestone 3+).
- No dedicated unit tests for the HTTP handler layer itself (only
  service-layer integration tests + one manual `curl` smoke test) — the
  handler status-code mapping (`handlers.go`'s `switch errors.Is(...)`
  blocks) is currently only exercised by the smoke test, not CI-run
  automated tests.
- Platform-administrator role (`users.platform_role`) exists as a column
  with no actual admin routes or authorization checks consuming it yet —
  Milestone 7's restricted administration surface.

## Milestone 3 — Backup engine and protocol: DONE

### `crates/backup-core`: DONE and verified

Real Rust library, not a stub — `scanner` (filesystem walk via `ignore`,
gitignore-style excludes, symlinks never followed, deterministic
sorted output), `chunker` (content-defined chunking via `fastcdc` v2020,
per-chunk AES-256-GCM encryption with content-hash-bound AAD, in-file
dedup via a seen-hash set), `crypto` (AES-256-GCM chunk/manifest
encryption, Argon2id vault-key wrapping under a user passphrase using
the *same* KDF parameters as the Go side's password hashing — m=19456,
t=2, p=1 — one consistent story about its KDF choice), `manifest`
(versioned, encrypted end-to-end, `format_version` checked on decrypt
and rejected if unsupported). 24 unit tests, all passing
(`cargo test -p backup-core`); clean `cargo clippy -p backup-core
--all-targets` and `cargo fmt --check -p backup-core`.

### Go API side: DONE and verified

- `backend/db/migrations/0004_backups`: `chunks` (content-addressed,
  deduplicated **per organization if the uploading device belongs to
  one, otherwise per individual user** — a deliberate scope narrower
  than global dedup, to avoid leaking cross-tenant content-equality;
  see the migration's comments and `docs/encryption.md`), `snapshots`
  (encrypted manifest stored directly as `BYTEA`, not in object
  storage; owning user/organization denormalized from the device at
  commit time), `snapshot_chunks` (join table, `ON DELETE RESTRICT` on
  `chunk_id` — a chunk referenced by any snapshot cannot be deleted out
  from under it; this is groundwork for future garbage collection, not
  GC itself, which is not implemented). Applied and rolled back
  cleanly against the real Postgres container.
- No `upload_sessions` table: an object's storage key is deterministic
  (dedup scope + content hash), so presigned URLs need no server-side
  session state — the same kind of deliberate scope reduction as
  organizations' no-ownership-transfer limitation.
- `backend/internal/storage`: thin `minio-go/v7` wrapper — presigned
  PUT/GET URL generation (15-minute expiry) and `Stat` (real
  `StatObject` existence + size check).
- `backend/internal/backups`: real implementation —
  - `NegotiateUpload`: for each content hash a device proposes, reports
    whether it's already known within that device's dedup scope, and
    if not, a presigned PUT URL to a deterministic object key.
  - `CommitSnapshot`: for every referenced chunk **not already known to
    the server, calls real object storage's `StatObject`** to verify it
    was actually uploaded — and uses *that* response's size, never the
    client's claimed size — before writing any row. A snapshot
    referencing a chunk that was never uploaded is rejected
    (`ErrChunkNotUploaded`), not silently trusted. A dedup-index
    conflict from a concurrent commit of the same (scope, hash) is
    handled by re-reading the winning row, not erroring.
  - `ListForDevice` / `ListForUser` / `Get` (returns the encrypted
    manifest plus a presigned download URL per referenced chunk, for
    restore).
  - Every mutating action writes an audit event.
- **Authentication split by who is actually acting**, not mounted
  uniformly: `GET /backups`, `GET /backups/{id}`, and `GET
  /devices/{deviceID}/backups` (viewing history) use the same
  user-session `Authenticate` middleware as organizations/devices,
  since a human is looking at a dashboard. `POST
  /devices/{deviceID}/backups/negotiate` and the commit route use
  `devices.AuthenticateDevice` (exported from `internal/devices`,
  identical Ed25519-derived-bearer-token + live-revocation check
  `Heartbeat` already used) instead, because the actual backup agent
  (`backup-cli`) has no user session — only a device keypair.
- **Verified against the real containerized API, real Postgres, and
  real MinIO** (not mocks): registered a user, enrolled and approved a
  device, ran a real Ed25519 challenge/response (small throwaway Go
  signer, since this needs real crypto), obtained a device access
  token, negotiated an upload (got `exists:false` + a presigned PUT),
  PUT real bytes directly to MinIO, committed a snapshot referencing
  that chunk, listed it both as the device and as the user, fetched
  snapshot detail, downloaded the chunk via the returned presigned GET
  URL, and **diffed the downloaded bytes against the original —
  byte-for-byte match**. Also verified: negotiating the same hash again
  correctly reports `exists:true` with no upload URL (dedup works);
  committing a snapshot that references a hash that was never uploaded
  is rejected with `chunk_not_uploaded` (the server does not trust an
  unverified client claim).
- Found and fixed during that verification: `CreateChunk`'s `ON
  CONFLICT` initially targeted the dedup-scope index by name (`ON
  CONFLICT ON CONSTRAINT ...`), which Postgres rejects for a plain
  `CREATE UNIQUE INDEX` that isn't a named constraint — fixed to infer
  the index by its exact expression list (`ON CONFLICT (COALESCE(...),
  content_hash)`), re-verified end-to-end afterward.

### `crates/backup-cli`: DONE and verified

Real end-to-end Rust client (`reqwest` + `ed25519-dalek` + `backup-core`),
not a stub — four subcommands:

- `init`: logs in as a user, generates an Ed25519 device keypair, enrolls
  and self-approves the device, generates a fresh `VaultKey`, wraps it
  under a user-supplied passphrase, writes `{device_id, signing key seed,
  wrapped vault key}` to a local state file (`0600` permissions on Unix).
- `backup <dir>`: scans, chunks, and encrypts a directory; deduplicates
  **across every file in the run**, not just within one (`pack()` only
  dedups within a single call — `backup-cli` merges by hash across calls
  before negotiating); authenticates as the device (Ed25519
  challenge/response); negotiates which chunks the server is missing;
  uploads only those directly to object storage via the returned
  presigned PUT URLs; commits the snapshot.
- `list`: lists the logged-in user's snapshots.
- `restore --snapshot <id> --to <dir>`: logs in as a user, fetches the
  snapshot's encrypted manifest and a presigned GET URL per chunk,
  decrypts everything, and reconstructs files — rejecting any manifest
  path that would escape the target directory (`main.rs::safe_join`), a
  real check against a crafted/corrupted manifest, not just a comment.
- A `--resolve HOST:IP:PORT` flag (repeatable, same idea as curl's
  `--resolve`) so the client can run on the host and still reach a
  presigned URL that embeds Docker Compose's internal `minio` hostname —
  see `docs/backup-protocol.md`'s limitations for why this is a
  local-dev-only wrinkle, not a protocol issue.

Clean `cargo build`, `cargo clippy --all-targets`, `cargo fmt --check`
across the whole workspace.

**Verified by actually running the compiled binary** (not curl standing
in for it) against the real containerized stack: `init` → `backup` on a
real directory with nested subdirectories, duplicate file content across
two files, and an excluded `node_modules/` pattern → `list` → `restore`
into a fresh directory → diffed byte-for-byte identical, confirmed the
excluded directory did not round-trip, confirmed the duplicate content
produced only one uploaded chunk (dedup). This exact flow is now
automated: `scripts/test-e2e-backup.sh`, invoked by `make test-e2e`,
registers a throwaway user, runs it, and cleans up after itself — it is
no longer a manual, one-off check.

### Go integration tests: DONE

`backend/tests/integration/backups_test.go` (build tag `integration`),
against a real disposable Postgres **and real MinIO** — not mocks, since
the property this package most needs proven ("a snapshot cannot
reference a chunk that was never uploaded") only means anything against
a real object store:

- Full negotiate → upload → commit → re-negotiate (dedup) → get → restore
  flow, with an HTTP PUT/GET against the real presigned URLs.
- A commit referencing a never-uploaded hash is rejected
  (`ErrChunkNotUploaded`).
- Dedup is scoped per user, not global: two unrelated users uploading
  identical content both get `exists:false` — directly exercises the
  privacy property `0004_backups.up.sql`'s comments describe.
- A different user cannot read or list another user's snapshot
  (`ErrNotFound`, tenant isolation).

All passing. Run:
`DATABASE_URL=... go test -tags=integration -race ./tests/integration/...`

### Explicit limitations / not yet done (do not assume otherwise)

- **No multi-device vault sharing.** Each `backup-cli init` generates an
  unrelated `VaultKey` — two devices for the same user have two
  unconnected vaults today. See `docs/encryption.md`.
- **No incremental backups, no upload retry/resume within one run.** See
  `docs/backup-protocol.md`'s limitations section for each. (Chunk
  garbage collection and snapshot deletion, also originally listed here
  as missing, are now implemented — see Milestone 4 below.)
- A user with committed snapshots still cannot be **hard**-deleted — see
  Milestone 4's note on why `DELETE /api/v1/me` deliberately deactivates
  an account instead of deleting the row. The cascade-ordering hazard
  that motivated that choice (`chunks.user_id`'s own `CASCADE` racing
  `snapshot_chunks.chunk_id`'s `ON DELETE RESTRICT`) is real but no
  longer blocking, since nothing calls a hard delete.
- No dedicated unit tests for `internal/backups`' HTTP handler layer
  itself (only service-layer integration tests + the manual/e2e
  verification above) — same gap identity/organizations/devices already
  have, noted there too.
- `backup-cli`'s device state file stores its Ed25519 private key
  unencrypted (filesystem permissions only, like an SSH key) — see
  `docs/encryption.md`.

## Milestone 4 — Storage quotas, retention, and background jobs: DONE

Scope was confirmed with the user before starting (background jobs:
quotas/retention/scheduler), not assumed from the inference in the
previous version of this section.

### Schema: DONE and verified

`backend/db/migrations/0005_plan_limits`: adds `storage_quota_bytes`
(default 5 GiB) and `retention_keep_snapshots` (default 10) to both
`users` and `organizations` — plain mutable columns per account, not a
separate plans table (there is exactly one limit of each kind today) and
not read from process config (so a future admin surface can grant one
account a bigger quota without a restart). Applied and rolled back
cleanly against the real Postgres container.

### `internal/usage` and `internal/quotas`: DONE and verified

- `usage.Service`: current storage usage per scope is a live
  `SUM(ciphertext_size)` over `chunks`, computed on demand rather than a
  separately-maintained counter that could drift from reality — fine at
  this project's scale.
- `quotas.Service.EnsureCapacity`: checks usage + the bytes a commit is
  about to add against the scope's `storage_quota_bytes`, returning
  `ErrQuotaExceeded` if it would go over. The *same* scope dedup already
  uses (per-organization-if-the-device-has-one, else per-user — see
  Milestone 3) is what quota is checked against, so "how much a device
  may still upload" and "what it's actually limited against" can never
  disagree.
- Wired into `backups.Service.CommitSnapshot` **before** any new chunk
  row is written: every not-already-known chunk is `Stat`-verified and
  its real size summed first; only if the total fits does any row get
  created. A rejected commit leaves zero new chunk rows behind — verified
  by `TestQuotas_CommitOverQuotaIsRejectedAndLeavesNoNewChunk`, which
  re-negotiates after a rejected commit and confirms the chunk is still
  reported missing. `POST .../backups` maps `ErrQuotaExceeded` to
  `507 Insufficient Storage`, `quota_exceeded`.

### Snapshot deletion and real chunk garbage collection: DONE and verified

- `backups.Service.DeleteSnapshot` (user-session-authed, ownership
  checked exactly like `Get`) and `DeleteSnapshotForRetention` (no
  per-call authz — the caller is the retention sweep, acting on its own
  policy authority, not a user's request) share one internal
  `deleteSnapshotAndGC`: unlink + delete the snapshot row
  transactionally, then — outside that transaction, since it's real
  network I/O — for every chunk that was only reachable through it,
  remove the object from MinIO **and** delete the chunk row. This is the
  first place anything in this project actually shrinks object storage;
  everything before this milestone only ever added to it.
- `DELETE /api/v1/backups/{id}` is new, user-facing, and shares this
  exact mechanism with automatic retention. **Verified against the real
  containerized API**: an outsider's `DELETE` on another user's snapshot
  returns 404 (not confirming the snapshot exists); the owner's `DELETE`
  succeeds; a subsequent `GET` of the same snapshot then also 404s; the
  chunk row count for that user dropped to zero afterward.

### `internal/retention` and `cmd/worker`: DONE and verified

- `retention.Service.Sweep`: for every device that has ever committed a
  snapshot, keeps the most recent `retention_keep_snapshots` (resolved
  from the device's owning user or organization) and deletes the rest via
  `DeleteSnapshotForRetention`. A failure sweeping one device is logged
  and skipped, not fatal to the whole pass.
- `cmd/worker` now does something: builds the same storage/usage/quotas/
  backups dependency chain the API does, runs one sweep immediately at
  startup (so a freshly deployed worker doesn't sit idle for a full
  interval first), then on a ticker — `RETENTION_SWEEP_INTERVAL`
  (default `1h`) — until shutdown. **Verified against the real running
  worker container**: logs show `retention_sweep_completed` immediately
  on startup.
- 2 integration tests against real Postgres + MinIO: a sweep with 3
  snapshots and `retention_keep_snapshots=2` deletes the oldest one and
  confirms its chunk's object is actually gone from MinIO (`Stat` returns
  `storage.ErrNotFound`), not just unlinked in the database; a sweep with
  an account under its limit changes nothing. Both passing.

### Explicit limitations / not yet done (do not assume otherwise)

- **No per-scope quota/retention admin UI or API** — the new columns are
  only ever changed directly in the database today (as the tests above
  do). An admin endpoint to adjust them is Milestone 7 territory
  (`users.platform_role` already exists for exactly this, unused so far).
- **Retention is a single global policy knob per account**, not
  per-device or per-snapshot-tag (e.g. "keep monthly snapshots longer").
  `retention_keep_snapshots` applies uniformly to every device the
  account owns.
- **A quota check can theoretically be raced** by two concurrent commits
  to the same scope each passing `EnsureCapacity` before either writes
  its chunk rows, momentarily landing slightly over quota. Accepted at
  this project's scale (personal backups, not adversarial multi-tenant
  billing) — the same kind of accepted race the dedup-conflict handling
  already has, resolved by a retry/next-sweep rather than a lock.
- **No HTTP route to change `RETENTION_SWEEP_INTERVAL` or trigger a sweep
  on demand** — only the ticker and the one at-startup run.

## Hardening pass (post-Milestone 4): DONE

Not a numbered milestone — closing out specific, previously-documented
limitations rather than building new scope, per explicit user direction
("harden what exists" was one of the offered options; the user picked
this over jumping to a UI milestone). All three items below were
pulled directly from earlier "Explicit limitations" sections, not newly
discovered:

- **Org-scoped device approval now requires owner/admin role** — see
  Milestone 2's devices section above for what changed and how it's
  verified.
- **`PATCH` and `DELETE /api/v1/me` are implemented** — see Milestone 2's
  identity limitations above for the soft-delete design and its
  verification.
- Both are covered by integration tests against a real database (6 new
  tests total across `devices_test.go` and `identity_test.go`) and the
  `DELETE`/`PATCH /me` pair additionally verified against the real
  containerized API with `curl`.

Deliberately NOT attempted in this pass (would need something this
environment doesn't have, or would open a much larger scope than
"harden what exists" implies):

- A real email provider (needs third-party credentials).
- Redis-backed rate limiting (needs a new infrastructure dependency).
- An independently rotatable MFA secret encryption key.
- No dedicated HTTP-handler-layer unit tests were added (still relying on
  service-layer integration tests + manual/curl verification, same gap
  noted in every milestone so far).

## Milestone 6 — Web dashboard: DONE for the scope below

Scope was confirmed with the user (web dashboard, not desktop app or
further backend hardening) before starting, then extended in a second
pass that also confirmed with the user: finish organizations/security/
storage UI, and build a separate Go operator CLI (`deepctl-admin`) plus a
cross-platform release pipeline instead of an admin web UI.

### Done and verified

- Monorepo wiring: root `package.json` + `pnpm-workspace.yaml`
  (`apps/*`, `packages/*`), `apps/web` as a real pnpm workspace member.
  `infra/docker/web.Dockerfile` (written in Milestone 1, unbuildable
  until now) builds successfully and its nginx runtime serves correctly,
  including SPA client-side routing via `try_files` (`/backups` returns
  200, not a raw 404, confirmed with a real build + `docker run`). Wired
  into `compose.yaml` as the `web` service (port 5173, matching
  `CORS_ALLOWED_ORIGINS`) and into `Makefile`'s `build`/`lint`/
  `typecheck` targets (previously placeholders saying no frontend
  existed) plus a new `dev-web` target for the Vite dev server. Added a
  `web` CI job (typecheck + lint + build) and fixed the pre-existing
  `backend-integration` CI job, which had never actually been run (no
  `origin` remote configured) and would have failed as written — it used
  GitHub Actions' native `services:` for Postgres but had no MinIO at
  all, even though this session's own backups/quotas/retention
  integration tests require a real one. Fixed by reusing
  `compose.yaml`'s own `minio`/`minio-init` definitions as a CI step
  (GitHub's native service containers can't be given MinIO's required
  `server /data` command) rather than reinventing MinIO startup in YAML.
- `apps/web`: Vite + React 18 + TypeScript (strict), React Router.
  Hand-written API client (`src/lib/apiClient.ts`) and types
  (`src/lib/types.ts`) mirroring the Go API's actual JSON shapes — not
  generated from `backend/api/openapi.yaml`, which is stale (see
  limitations below). Bearer access token held in memory only (never
  `localStorage`); only the refresh token is persisted, and a 401 is
  retried exactly once after a transparent refresh.
  Pages: login (including the MFA-code step `mfa_required` triggers),
  register (enumeration-resistant — same "check your email" result
  either way, matching the API), devices (list/approve/revoke — **no
  enroll button**, since enrolling is `backup-cli init`'s job, requiring
  an Ed25519 keypair this dashboard deliberately does not generate),
  backups (list/delete, snapshot detail showing metadata and the exact
  `backup-cli restore` command to run — see the restore limitation
  below), account (display name, deactivation), **security** (password
  change; MFA enroll — secret + `otpauth://` URI, no QR code rendering,
  no new dependency added for one — confirm, recovery codes shown once,
  disable; session list + revoke-one + revoke-others), **organizations**
  (list, create, rename, member list with email/display name — the
  underlying `ListMembers` API originally returned bare user IDs with no
  way to show who's who, so a joined query/`Member.Email`+`DisplayName`
  were added — invite, role change, remove, leave, accept-invitation-by-
  token), and **storage** (usage-vs-quota bar per personal scope and per
  organization, via a new `GET /api/v1/usage` endpoint — nothing
  exposed `internal/usage` over HTTP before this).
- **Verified against the real containerized API** (CORS preflight from
  `http://localhost:5173`, and the exact request/response shapes every
  page's code calls — register, login, `/me`, devices, backups,
  snapshot detail, `PATCH /me`, organizations, members, usage, MFA — all
  traced with `curl` using real data). The MFA flow in particular was
  verified with a **real TOTP code**, not just a shape check: a Python
  snippet computed a genuine HMAC-SHA1 TOTP value from the enrolled
  secret, which the API accepted, confirming `confirmMfa`/`enrollMfa`'s
  wire format is actually correct end-to-end, and a subsequent login
  with `mfa_code` set to that real code succeeded while a wrong code
  correctly failed. `tsc -b --noEmit` (strict), `eslint` (0 errors, 1
  harmless `react-refresh` warning on a standard Context+hook
  co-location), and a production build all pass.
  **Not verified in an actual browser** — no browser-automation tool was
  available in this session. The dev server (`make dev-web`) and the
  compose-built production container (`docker compose up web`, port
  5173) were both left running/buildable for the user to open and click
  through themselves; this is a real, stated limitation, not a verified
  "works in a browser" claim.

### Explicit limitations / not yet done (do not assume otherwise)

- **No in-browser restore/decryption.** Restoring requires the vault
  passphrase and client-side AES-256-GCM/Argon2id — implemented only in
  Rust (`backup-core`). Porting that to TypeScript (or compiling
  `backup-core` to WASM) was deliberately not attempted here; the
  snapshot detail page shows metadata and the exact `backup-cli restore`
  command instead of a fake or partial in-browser decrypt.
- **No device enrollment from the browser** — devices are enrolled by
  `backup-cli init` (it generates the Ed25519 keypair). The dashboard
  only manages devices that already exist.
- **No MFA QR code** — the enroll step shows the raw secret and the
  `otpauth://` URI as text, not a scannable QR code. Adding one would
  mean a new dependency for a single feature; skipped deliberately, not
  an oversight.
- **No admin UI** — by explicit choice, not an oversight: platform
  administration (quotas, retention, suspend/activate, inspecting any
  account) is `deepctl-admin` (see below), a CLI, not a web page. The
  pre-existing empty `features/admin` directory stays empty.
- **`packages/{api-client,shared-types,ui,config}` are still empty.**
  `apps/web` is self-contained (its own `lib/apiClient.ts`,
  `lib/types.ts`, inline components) rather than split into these
  packages — the same reasoning `internal/memberships` was left empty
  for: splitting code into a shared package before a second consumer
  (e.g. `apps/desktop`) exists to justify the boundary adds workspace
  plumbing for no real benefit yet. Revisit when `apps/desktop` is
  actually built.
- A new feature folder, `features/account` (display name, deactivation),
  was used instead of the originally-scaffolded `features/security` —
  `security` ended up meaning MFA/sessions/password specifically once
  built, and profile/deactivation didn't fit there. Called out rather
  than silently diverging from the pre-existing scaffold.
- **`backend/api/openapi.yaml` was stale; fixed in a follow-up pass.**
  It documented only `/status` despite every route built in
  Milestones 2–6. Now documents all 47 operations across
  Auth/Me/Devices/Backups/Organizations/Usage, hand-written from the
  actual Go handler/request/response structs (not the reverse — the
  file says so explicitly, so the next person who edits a handler
  without updating this file knows which one is authoritative).
  `apps/web`'s types remain hand-written rather than generated from this
  spec — regenerating them now that the spec is accurate is a
  reasonable follow-up, not done in this pass.
- No automated frontend tests (unit/component/e2e) — `apps/web/src/tests`
  stays an empty directory. Verification so far is typecheck + lint +
  build + manual API-contract tracing with `curl`, not automated browser
  tests.

## Operator CLI (`deepctl-admin`) and cross-platform releases: DONE

Built instead of an admin web UI — see Milestone 6's "No admin UI" note
above for why. Scope confirmed with the user: a Go CLI, plus a `gh`-based
release workflow that cross-compiles it for Windows/macOS/Linux from one
set of source.

### Done and verified

- `backend/cmd/deepctl-admin`: a new fourth binary alongside `cmd/api`,
  `cmd/worker`, `cmd/migrate`. Connects to Postgres directly with the
  same DATABASE_URL-level trust `cmd/migrate` already assumes — **no
  user-session authorization check**, because the caller already has
  full database access by construction; this is an explicit, documented
  design choice, not an oversight. Commands: `users
  list/show/set-quota/set-retention/suspend/activate/deactivate`, `orgs
  list/show/set-quota/set-retention`, `retention sweep` (an on-demand
  run of the same sweep `cmd/worker` does on a timer), `usage <email>`.
  Every mutating command writes an audit event (`admin.*` event types),
  consistent with every other mutating action in this codebase.
- New `identity.Service` methods (`AdminListUsers`, `AdminGetUserByEmail`,
  `AdminSetStorageQuota`, `AdminSetRetentionLimit`, `AdminSetStatus`) and
  matching `organizations.Service` methods
  (`AdminListOrganizations`, `AdminGetByID`, `AdminSetStorageQuota`,
  `AdminSetRetentionLimit`) — real service methods with audit logging,
  not raw SQL in the CLI, so quota/retention/status changes go through
  the same code path and leave the same trail regardless of whether they
  came from `deepctl-admin` or (eventually) an HTTP admin route.
- `organizations.ListMembers` was changed to join `users` for email/
  display name (`ListMembersWithUser`) — the old query returned bare
  UUIDs, not useful for a human looking at a members list in either
  `deepctl-admin` or the web dashboard above.
- **Verified against the real containerized API/database**: registered a
  test user, ran `deepctl-admin users list/show/set-quota/set-retention`,
  confirmed the values stuck via `users show`; `deepctl-admin users suspend`
  followed by a real login attempt returned `403 account_suspended`;
  `deepctl-admin users activate` followed by login then succeeded (`200`);
  `deepctl-admin usage` reported real figures matching the quota just set;
  `deepctl-admin retention sweep` ran standalone (0 deleted, correctly, since
  no test snapshots existed); `deepctl-admin orgs list` showed real
  organization rows.
- `.github/workflows/release.yml`: triggered by pushing a `v*.*.*` tag.
  Cross-compiles `deepctl-admin` for linux/amd64, linux/arm64, darwin/amd64,
  darwin/arm64, and windows/amd64 — all from one `ubuntu-latest` runner,
  no per-OS runners or cross toolchain needed, since `deepctl-admin` only
  depends on pgx/v5 and minio-go, both pure Go with `CGO_ENABLED=0`.
  Packages each as `.tar.gz` (`.zip` for Windows), then a second job
  downloads all five and runs `gh release create <tag> <files...>
  --generate-notes` to publish them as a GitHub release.
  **Verified locally**: all five `GOOS`/`GOARCH` combinations actually
  built successfully on this machine (`go build` with each target's env
  vars set, matching exactly what the workflow's matrix does) — the
  workflow itself has not been run on real GitHub Actions, same
  pre-existing gap as the rest of this repo's CI (no `origin` remote
  configured here).

### Explicit limitations / not yet done (do not assume otherwise)

- `deepctl-admin` has no automated tests of its own (no `cmd/deepctl/*_test.go`)
  — verification was the manual run described above. The service methods
  it calls (`AdminSetStorageQuota` etc.) are covered only by that same
  manual run too, not a dedicated integration test file.
- No code-signing/notarization for the macOS or Windows binaries the
  release workflow produces — they will trigger an unsigned-binary
  warning on first run on those platforms. Not attempted: needs a paid
  Apple Developer / code-signing certificate this environment doesn't
  have.
- The release workflow has not actually been exercised on GitHub Actions
  (no tag has been pushed to a real `origin` remote from this
  environment) — only the build matrix's five `go build` invocations
  were verified locally, not the `gh release create` step or the
  artifact-passing between jobs.

## `backend/api/openapi.yaml`: fixed, DONE

Previously documented only `/status` (Milestone 1) despite every route
built since. Rewritten to cover all 47 operations actually implemented
across Auth, Me, Devices, Backups, Organizations, and Usage, with
request/response schemas transcribed directly from the Go handler
structs — including error codes (`mfa_required`, `account_suspended`,
`chunk_not_uploaded`, `quota_exceeded`, `last_owner`, etc.) and
auth-model notes that aren't obvious from the schema alone (which routes
are unauthenticated by design — device challenge/authenticate — and
which expect a device-scoped bearer token rather than a user session —
negotiate/commit/heartbeat).

**Verified**: `npx @redocly/cli lint backend/api/openapi.yaml` — 0
errors (one real bug was caught and fixed: an unquoted YAML flow-mapping
description string with a comma in it was silently parsed as an extra
mapping key), 23 remaining warnings are either "operation should
document a 4xx" for simple reads with no real failure mode beyond the
global 401, or "info object should have a license field" — both
stylistic, not structural. This is a different, stricter tool than the
repo's existing CI check (`char0n/swagger-editor-validate`, OpenAPI
3.0-oriented and never actually run — no `origin` remote here), so a
clean `redocly lint` result is strong but not identical evidence that
the CI job itself would pass.

### Explicit limitations / not yet done

- `apps/web`'s TypeScript types remain hand-written, not generated from
  this now-accurate spec. Worth doing as a follow-up (e.g.
  `openapi-typescript`) now that the spec is trustworthy, but not
  attempted in this pass.
- The existing CI job's actual validator
  (`char0n/swagger-editor-validate`) has still never run against this
  file on real GitHub Actions.

## Dev environment: email inbox, seed data, full walkthrough: DONE

Built so the user could actually click through the project locally
rather than only seeing it verified via `curl`. Scope confirmed with the
user (plan-mode design review) before starting.

### Done and verified

- **Dev Inbox**: `internal/notifications.InboxSender` logs exactly like
  `DevLogSender` and additionally keeps the most recent 200 "sent"
  emails in memory (mutex-protected, lost on restart — by design, this
  is a few-minutes-of-use tool, not a mailbox). Exposed, unauthenticated
  by necessity, at `GET /api/v1/dev/emails`
  (`internal/notifications/handlers.go`). **Two independent gates, not
  one**: `cmd/api` only constructs `InboxSender` (instead of
  `DevLogSender`) and only passes the handlers into `httpserver.Deps`
  when `cfg.Env == "development"`; `router.go` only mounts the route
  when that dependency is non-nil. Either gate alone already prevents
  exposure outside development.
- Web dashboard: `/dev/inbox` (new top-level route, same unauthenticated
  tier as `/login`/`/register`) lists captured emails, polling every 3s,
  with one-click actions matched by subject — "Verify email", inline
  "Reset password", "Copy invitation token" (which points at the
  already-built Accept-an-invitation form on the Organizations page
  rather than duplicating it). Linked from Login/Register. Token
  extraction mirrors the exact heuristic
  `backend/tests/integration`'s own `extractToken` test helper already
  used — same "word ending in `:`, take the next word" logic, now also
  implemented client-side.
- `backend/cmd/seed`: creates `alice`/`bob`/`carol`@example.test
  (password `DevPassword123!`) and one organization ("Acme Dev Org",
  alice=owner, bob=admin, carol=member) for a SaaS-style multi-account
  demo. Idempotent (checks `identity.Service.AdminGetUserByEmail`
  first, added for `deepctl-admin`, before creating anything). **Refuses to
  run if `cfg.Env == "production"`** — a hard check, not a suggestion,
  since this tool creates rows with a publicly-known password.
  Deliberately bypasses the email-verification and invite/accept email
  ceremonies (direct `MarkEmailVerified`/`CreateMembership` calls) —
  seed data should be immediately usable; the walkthrough doc exercises
  the real ceremonies separately, for a fresh, non-seeded account.
  `make seed` now runs it instead of the old "not yet implemented" stub.
- `docs/local-dev-walkthrough.md` (new): seed → log in → register a
  *real* new account and verify it via the Dev Inbox → create, view,
  restore, and delete a real backup via `backup-cli` → forgot-password
  via the Dev Inbox → the full invite/accept organization flow via a
  Dev-Inbox-copied token → account deactivation.
- **Verified**: fresh `docker compose up -d --build` (full stack);
  `make seed` run twice — first run creates everything, second reports
  "already exists" for all three accounts and the org, no duplicate
  rows; a brand-new account registered via `curl`, its real
  verification email confirmed present at `GET /api/v1/dev/emails` with
  the real token, `POST /auth/verify-email` with that token succeeding,
  then login succeeding — the same loop the walkthrough doc tells a
  human to click through, run via the API directly instead. Same loop
  repeated for forgot-password. `go build`/`vet`/`gofmt`, `pnpm
  typecheck`/`lint`/`build` all clean.

### Explicit limitations / not yet done

- Still no browser-automation tool available this session — the
  Dev Inbox page's actual click-through was verified by driving the
  same underlying API calls directly, not by clicking a real browser.
- Captured emails are in-memory and per-process: restarting the `api`
  container (or running multiple API replicas) loses/fragments the
  inbox. Acceptable for a single-instance local dev loop; would need a
  shared store to matter beyond that, which is out of scope for a
  dev-only tool.
- `cmd/seed` does not create any devices or backups — creating those
  needs `backup-cli` (Rust, real chunking/encryption), which a Go seed
  binary can't easily drive. The walkthrough doc has the user do this
  step themselves instead of pre-seeding it invisibly.

## Next task

No further milestone has been scoped or confirmed with the user yet.
Likely candidates: an actual browser click-through of the web dashboard
(needs the user, or a browser-automation tool this session didn't have),
actually pushing a `v*.*.*` tag to verify `release.yml` end-to-end on
real GitHub Actions (needs an `origin` remote, not configured here),
Milestone 5 (desktop app, `apps/desktop` — still directory scaffolding
only), generating `apps/web`'s types from the now-accurate OpenAPI spec,
or continuing to harden the backend (real email provider, Redis rate
limiting, MFA key rotation — see Milestone 4's hardening-pass section
above). Confirm with
the user before starting any of these.
