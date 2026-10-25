#!/usr/bin/env bash
# Real end-to-end test of Milestone 3's backup protocol: registers a
# throwaway user, runs the actual backup-cli binary (not a mock, not a
# curl simulation of it) through init -> backup -> list -> restore against
# the live Docker Compose stack (real Postgres, real MinIO, real API),
# and asserts the restored tree is byte-for-byte identical to the
# original, that an excluded file did not round-trip, and that dedup
# worked (one upload, not two, for duplicate content). Invoked by
# `make test-e2e`; assumes the stack is already up (the Makefile target
# brings it up first).
set -euo pipefail

SERVER="${SERVER:-http://localhost:8080}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
EMAIL="e2e-backup-$(date +%s)-$$@example.test"
PASSWORD='CorrectHorseBatteryStaple9!'
PASSPHRASE='e2e-test-vault-passphrase'

cleanup() {
  status=$?
  set +e
  if [ -n "${POSTGRES_USER:-}" ] && [ -n "${POSTGRES_DB:-}" ]; then
    docker exec deeparchive-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -q -c "
      DELETE FROM snapshot_chunks USING snapshots WHERE snapshot_chunks.snapshot_id = snapshots.id AND snapshots.user_id = (SELECT id FROM users WHERE email = '$EMAIL');
      DELETE FROM snapshots WHERE user_id = (SELECT id FROM users WHERE email = '$EMAIL');
      DELETE FROM chunks WHERE user_id = (SELECT id FROM users WHERE email = '$EMAIL');
      DELETE FROM users WHERE email = '$EMAIL';
    " >/dev/null 2>&1
  else
    # Not run through `make` (which sources .env and exports it) — cannot
    # reach Postgres to clean up, so the test user is left behind. Warn
    # loudly rather than silently leaking test data.
    echo "WARNING: POSTGRES_USER/POSTGRES_DB not set, skipped deleting test user $EMAIL — run via 'make test-e2e', or clean up manually" >&2
  fi
  rm -rf "$WORKDIR"
  exit $status
}
trap cleanup EXIT

echo "==> registering throwaway test user $EMAIL"
curl -sf -X POST "$SERVER/api/v1/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\",\"display_name\":\"E2E Backup Test\"}" >/dev/null

echo "==> building a source tree with nested dirs, duplicate content, and an excluded dir"
SRC="$WORKDIR/src"
RESTORE="$WORKDIR/restore"
mkdir -p "$SRC/docs" "$SRC/notes" "$SRC/node_modules"
echo "hello from the e2e backup test" >"$SRC/root.txt"
# Must stay under FastCDC's min_size (256 KiB, see ChunkerConfig::default)
# so this is *guaranteed* to be exactly one chunk regardless of its random
# content — FastCDC never cuts before min_size. At or above min_size, a
# random blob's cut points become content-dependent and the chunk count
# below would be flaky.
head -c 200000 /dev/urandom >"$SRC/docs/random.bin"
echo "duplicate content to exercise dedup" >"$SRC/notes/a.txt"
cp "$SRC/notes/a.txt" "$SRC/notes/b.txt"
echo "must not be backed up" >"$SRC/node_modules/dep.js"

CARGO="cargo run -q -p backup-cli --manifest-path $REPO_ROOT/crates/Cargo.toml --"
STATE="$WORKDIR/device.json"
# --resolve stands in for real DNS here: a presigned MinIO URL embeds the
# Docker Compose-internal hostname "minio", which only the API container
# resolves by default. See backup-cli --help and docs/backup-protocol.md.
COMMON=(--server "$SERVER" --state "$STATE" --resolve minio:127.0.0.1:9000)

echo "==> backup-cli init"
$CARGO "${COMMON[@]}" init --email "$EMAIL" --password "$PASSWORD" --name e2e-test-device --os linux --passphrase "$PASSPHRASE"

echo "==> backup-cli backup"
BACKUP_OUTPUT="$($CARGO "${COMMON[@]}" backup "$SRC" --passphrase "$PASSPHRASE" --exclude 'node_modules/')"
echo "$BACKUP_OUTPUT"
if ! echo "$BACKUP_OUTPUT" | grep -q "uploaded 3 new chunks (0 already present)"; then
  echo "FAIL: expected exactly 3 new chunks (root.txt, random.bin, and ONE copy of the duplicated notes/*.txt content) to upload" >&2
  exit 1
fi
SNAPSHOT_ID="$(echo "$BACKUP_OUTPUT" | sed -n 's/^snapshot committed: //p')"
if [ -z "$SNAPSHOT_ID" ]; then
  echo "FAIL: could not find a committed snapshot id in backup-cli output" >&2
  exit 1
fi

echo "==> backup-cli list"
$CARGO "${COMMON[@]}" list --email "$EMAIL" --password "$PASSWORD" | grep -q "$SNAPSHOT_ID"

echo "==> backup-cli restore"
$CARGO "${COMMON[@]}" restore --email "$EMAIL" --password "$PASSWORD" --passphrase "$PASSPHRASE" \
  --snapshot "$SNAPSHOT_ID" --to "$RESTORE"

echo "==> verifying byte-for-byte match"
if ! diff -rq "$SRC/root.txt" "$RESTORE/root.txt" >/dev/null; then
  echo "FAIL: root.txt does not match" >&2; exit 1
fi
if ! diff -rq "$SRC/docs" "$RESTORE/docs" >/dev/null; then
  echo "FAIL: docs/ does not match" >&2; exit 1
fi
if ! diff -rq "$SRC/notes" "$RESTORE/notes" >/dev/null; then
  echo "FAIL: notes/ does not match" >&2; exit 1
fi
if [ -e "$RESTORE/node_modules" ]; then
  echo "FAIL: excluded node_modules/ was restored — exclude patterns did not work" >&2; exit 1
fi
if [ "$(shasum -a 256 <"$SRC/docs/random.bin" | cut -d' ' -f1)" != "$(shasum -a 256 <"$RESTORE/docs/random.bin" | cut -d' ' -f1)" ]; then
  echo "FAIL: random.bin checksum mismatch" >&2; exit 1
fi

echo "==> PASS: backup/restore round trip is byte-for-byte identical, dedup worked, excludes worked"
