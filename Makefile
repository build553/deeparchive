.PHONY: dev dev-web build test test-unit test-integration test-e2e lint typecheck migrate-up migrate-down seed clean

ENV_FILE ?= .env
-include $(ENV_FILE)
export

## dev: start infrastructure + API + worker via Docker Compose
dev:
	docker compose up --build

## build: build all backend binaries and the web dashboard
build:
	cd backend && go build ./...
	pnpm --filter web build

## dev-web: start the web dashboard's Vite dev server (expects the backend
## stack — `make dev` — already running separately)
dev-web:
	pnpm --filter web dev

## test: run everything that does not require extra setup, and say so
test: test-unit
	@echo ""
	@echo "NOTE: 'make test' ran unit tests only."
	@echo "Run 'make test-integration' for DB/storage integration tests (requires docker compose up postgres minio)."
	@echo "Run 'make test-e2e' for end-to-end tests (requires the full stack running)."

## test-unit: Go unit tests (no external services)
test-unit:
	cd backend && go test -race -short ./...

## test-integration: Go integration tests against a real disposable Postgres + MinIO
test-integration:
	@command -v docker >/dev/null || { echo "docker is required for integration tests" >&2; exit 1; }
	docker compose up -d postgres minio minio-init
	cd backend && DATABASE_URL="$${DATABASE_URL}" go test -race -tags=integration ./tests/integration/...

## test-e2e: end-to-end tests against the full running stack. Currently
## covers the backup/restore round trip (backup-cli against the real
## API/Postgres/MinIO, Milestone 3). Playwright web-UI e2e will be added
## here once apps/web exists (Milestone 6) — this target grows, it is not
## renamed again when that lands.
test-e2e:
	@command -v docker >/dev/null || { echo "docker is required for e2e tests" >&2; exit 1; }
	docker compose up -d postgres minio minio-init migrate api
	./scripts/test-e2e-backup.sh

## lint: static analysis for Go (golangci-lint if installed, else go vet) and the web dashboard
lint:
	cd backend && gofmt -l . | tee /tmp/deeparchive-gofmt.out; test ! -s /tmp/deeparchive-gofmt.out
	cd backend && go vet ./...
	@command -v golangci-lint >/dev/null && (cd backend && golangci-lint run) || echo "golangci-lint not installed; ran go vet only"
	pnpm --filter web lint

## typecheck: TypeScript typechecking for the web dashboard (apps/desktop
## has no code yet — still a later milestone)
typecheck:
	pnpm --filter web typecheck

## migrate-up: apply all pending migrations
migrate-up:
	cd backend && go run ./cmd/migrate up

## migrate-down: roll back the most recent migration
migrate-down:
	cd backend && go run ./cmd/migrate down

## seed: create dummy accounts + an organization for local dev testing
## (idempotent — safe to run more than once). See
## docs/local-dev-walkthrough.md.
seed:
	cd backend && go run ./cmd/seed

## clean: stop and remove local containers AND THEIR DATA VOLUMES
# WARNING: this deletes local Postgres and MinIO data irreversibly.
clean:
	docker compose down -v
