# Architecture

DeepArchive is a modular monolith: one Go backend codebase producing two
deployable processes (API, worker) that share domain logic and database
access, plus two separate frontends (desktop, web) and a Rust backup
engine embedded in the desktop app via Tauri.

## Components

```
                        ┌───────────────────┐
                        │   React Desktop    │  Tauri shell
                        │  (apps/desktop)    │  invokes Rust commands
                        └─────────┬───────────┘
                                  │ HTTPS (typed API client)
                        ┌─────────▼───────────┐        ┌──────────────────┐
                        │  React Web Dashboard │  HTTPS │   Go API process  │
                        │     (apps/web)       ├───────►│  (backend/cmd/api)│
                        └──────────────────────┘        └─────────┬────────┘
                                                                   │
                     direct, short-lived,  ┌───────────────────┐  │ pgx
                     narrowly-scoped       │   Rust backup      │  │
                     presigned S3 upload/  │   engine           │  │
                     download ◄────────────┤ (crates/backup-core)  │
                                            └───────────────────┘  │
                        ┌──────────────────────┐                  │
                        │   Go worker process   │◄─────────────────┘
                        │ (backend/cmd/worker)  │  shared internal/* packages
                        └───────────┬───────────┘
                                    │
                        ┌───────────▼───────────┐
                        │  PostgreSQL (authoritative state) │
                        └────────────────────────┘
                        ┌────────────────────────┐
                        │  S3-compatible storage   │
                        │  (MinIO in dev)          │
                        └────────────────────────┘
```

## Dependency direction (backend)

```
HTTP handlers (internal/httpserver + per-domain route files)
  -> domain services (internal/identity, internal/backups, internal/snapshots, ...)
  -> repositories (sqlc-generated queries in backend/db/generated)
  -> PostgreSQL (pgx) and S3 (AWS SDK v2) implementations
```

Handlers stay thin: parse/validate the request, call a domain service,
translate the result/error to an HTTP response. Business rules (quota
checks, authorization, state transitions) live in domain services so they
are unit-testable without an HTTP server, and so the worker process can
call the exact same logic as the API.

## Why large file transfers bypass the API

The Go API never proxies backup object bytes. It authorizes a backup job,
validates ownership/quota, and issues short-lived, narrowly-scoped
presigned S3 URLs; the Rust engine uploads/downloads directly against
object storage. This keeps the API stateless with respect to transfer
size and avoids the API becoming a bandwidth/memory bottleneck. See
`docs/backup-protocol.md` (written alongside Milestone 3) for the exact
upload-session lifecycle.

## Current implementation

See `docs/implementation-status.md` for what is actually built, tested,
and running versus still planned. As of this writing only Milestone 1
(foundation: config, health checks, HTTP server skeleton, Docker Compose
dev environment, migration tooling) is complete.
