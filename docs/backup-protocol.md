# Backup protocol

How `backup-cli` and the Go API talk to each other to back up and
restore a directory. Two different identities act on this API, not one
— see "Who authenticates as what" below before reading the endpoints,
since it explains why two structurally similar route groups use two
different `Authenticate` middlewares. For the cryptography referenced
throughout, see `docs/encryption.md`.

## Who authenticates as what

| Action | Identity | Why |
|---|---|---|
| Enroll/approve a device, list/view backups, restore | Logged-in **user** (session bearer token, `identity.Service.Authenticate`) | A human at a keyboard, in a browser or desktop app, is doing this. |
| Negotiate an upload, commit a snapshot | The **device** itself (`devices.AuthenticateDevice`, Ed25519-derived bearer token) | The actual backup agent runs unattended on a machine with no user session — only a device keypair, proven the same way `POST /devices/{id}/heartbeat` already does. |

Both checks re-verify live state in Postgres on every request (a revoked
session or a revoked device fails immediately, not just at token
expiry) — the same pattern used everywhere else in this API.

## One-time setup: enrolling a device (`backup-cli init`)

1. Log in as a user: `POST /api/v1/auth/login` → user session token.
2. Generate an Ed25519 keypair locally. The private key never leaves the
   device.
3. `POST /api/v1/devices/enroll` (user token) with the public key (base64)
   → device id, status `pending`.
4. `POST /api/v1/devices/{id}/approve` (user token) → status `active`.
5. Generate a random `VaultKey`, wrap it under a user-chosen passphrase
   (`WrappedVaultKey::wrap`), and write `{device_id, signing key seed,
   wrapped vault key}` to a local state file. This file plus the
   passphrase are the only things that can ever decrypt this vault's
   backups — see `docs/encryption.md`'s limitations.

Authenticating as the device afterward (needed before negotiate/commit)
is the same Ed25519 challenge/response flow devices already use for
`heartbeat`:

1. `POST /api/v1/devices/{id}/challenge` (no auth — the device has no
   session yet) → a random nonce.
2. Sign the nonce with the device's private key.
3. `POST /api/v1/devices/{id}/authenticate` with the signature → a
   short-lived device access token.

## Backing up (`backup-cli backup <dir>`)

1. **Scan**: `backup_core::scanner::scan` walks the directory,
   gitignore-style exclude patterns applied, symlinks never followed.
2. **Chunk + encrypt**: for each file, `backup_core::chunker::pack` splits
   it with FastCDC and AES-256-GCM-encrypts each distinct chunk under the
   vault key, deduplicating within the file. `backup-cli` additionally
   deduplicates **across every file in the run** (pack() only dedups
   within one call) before anything is sent to the server.
3. **Negotiate**: `POST /devices/{id}/backups/negotiate` (device token)
   with every distinct content hash from step 2. For each hash, the
   response says whether this device's dedup scope already has it, and
   if not, a presigned PUT URL to a deterministic object key
   (`chunks/<scope>/<hash>` — see `docs/encryption.md`'s dedup-scope
   section). No server-side "upload session" state is created here or
   anywhere else in this flow; the key is derivable from scope + hash
   alone, so the URL can be regenerated at any time.
4. **Upload**: PUT each missing chunk's ciphertext directly to its
   presigned URL — the API process is never in the data path.
5. **Commit**: `POST /devices/{id}/backups` (device token) with the
   encrypted manifest (nonce + ciphertext, base64) and the full list of
   referenced chunk hashes + their ciphertext sizes. The server:
   - For every hash it doesn't already have on record, calls real object
     storage's `StatObject` to confirm it was actually uploaded —
     **using the real response size, never the client's claimed size**
     — before writing anything. A commit referencing a chunk that was
     never uploaded is rejected (`chunk_not_uploaded`), not silently
     trusted. See `TestBackups_CommitRejectsAChunkThatWasNeverUploaded`.
   - Inserts the `snapshots` row and the `snapshot_chunks` links in one
     transaction.
   - If two commits race to register the same new `(scope, hash)` chunk,
     the database's unique index resolves it; the loser re-reads and
     uses the winner's row rather than erroring.
   - Before any of the above, the server sums the real sizes of every
     not-already-known chunk and checks that against the scope's
     `storage_quota_bytes` (the same scope dedup uses). Over quota means
     the whole commit is rejected — `507 Insufficient Storage`,
     `quota_exceeded` — with **zero** new chunk rows written, not a
     partial commit.

## Deleting a snapshot

`DELETE /api/v1/backups/{id}` (user token, ownership-checked like `GET`)
removes a snapshot and garbage-collects: any chunk the deleted snapshot
referenced that no *other* snapshot still references is removed from
object storage and the database, not just unlinked. The same mechanism
runs automatically and unattended: `cmd/worker` enforces each account's
`retention_keep_snapshots` limit on a timer (`RETENTION_SWEEP_INTERVAL`),
keeping each device's most recent N snapshots and deleting the rest this
same way. See `docs/implementation-status.md`'s Milestone 4 section for
what's verified.

## Restoring (`backup-cli restore --snapshot <id> --to <dir>`)

1. Log in as a user; `GET /api/v1/backups/{id}` (user token) returns the
   encrypted manifest plus a fresh presigned GET URL per referenced
   chunk.
2. Decrypt the manifest with the vault key.
3. For each chunk: GET its presigned URL, decrypt with the vault key
   using the chunk's hash as AAD (from the manifest's per-chunk nonce),
   verify by hash.
4. For each file entry: reassemble its plaintext from chunk references
   (`backup_core::chunker::unpack`) and write it under the target
   directory. The file's path is re-joined component-by-component,
   rejecting any `..` or absolute component (`main.rs::safe_join`) — an
   encrypted-end-to-end manifest is never validated by the server, so a
   corrupted or crafted one must not be able to write outside the chosen
   restore directory.

Verified end-to-end (not just by code review) via
`scripts/test-e2e-backup.sh` (`make test-e2e`): a real directory with
nested subdirectories, duplicate file content, and an excluded pattern,
backed up and restored through the real `backup-cli` binary against the
live Docker Compose stack, diffed byte-for-byte against the original.

## Known limitations (do not assume otherwise)

- **No incremental backups.** Every run re-scans, re-chunks, and
  re-encrypts the entire source tree from scratch. Storage/upload
  efficiency comes entirely from server-side content-hash dedup, not
  from skipping local work on unchanged files between runs.
- **Quota and retention limits are per-account, not finer-grained.**
  `retention_keep_snapshots` applies uniformly to every device an account
  owns; there's no per-device or per-tag policy (e.g. "keep monthly
  snapshots longer"), and both limits can only be changed directly in
  the database today — no admin UI or API yet.
- **Deleting a user who has committed snapshots is not supported** — see
  `docs/implementation-status.md`'s Milestone 3 limitations for why.
- **Local Docker Compose development needs `--resolve`.** A presigned
  MinIO URL embeds the hostname the API's object storage client was
  configured with (`minio`, a Compose-internal DNS alias). Running
  `backup-cli` from the host, rather than inside the Compose network,
  needs `--resolve minio:127.0.0.1:9000` (the same thing curl's
  `--resolve` does) to reach it. A real deployment behind one
  externally-routable S3-compatible endpoint does not have this
  wrinkle — it is a local-dev-only artifact of split internal/external
  Docker networking, not a protocol design flaw.
- **No upload retry/resume.** If `backup-cli backup` is interrupted
  mid-run, already-uploaded chunks are **not** wasted (a re-run's
  negotiate step will find them already present and skip them again),
  but there is no partial-progress tracking within a single run — an
  interrupted run must be restarted from the top.
