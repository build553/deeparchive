# Encryption and threat model

This describes what `crates/backup-core` and `backup-cli` actually
implement, not an aspiration. Claims here should match the code in
`crates/backup-core/src/crypto.rs` and `manifest.rs` — if they diverge,
the code is right and this file is stale.

## What the server never sees

The Go API and object storage (MinIO/S3) see:

- Opaque, content-addressed ciphertext chunks (`chunks/<scope>/<hash>`).
- An encrypted manifest blob (`snapshots.manifest_nonce` /
  `manifest_ciphertext`).
- Each chunk's plaintext SHA-256 hash and ciphertext size.
- A snapshot's total plaintext size and chunk count.

They never see: a plaintext byte, a file name, a directory structure, a
file's permission bits, or the vault key. Restoring, listing file names,
or decrypting anything requires the user's passphrase, which the server
never receives.

## Key hierarchy

```
passphrase --(Argon2id)--> KEK --(AES-256-GCM)--> unwraps --> VaultKey
                                                                  |
                                                    encrypts chunks + manifest
```

- **VaultKey**: a random 256-bit key (`VaultKey::generate`,
  `getrandom`-sourced), generated once by `backup-cli init`. Everything in
  one backup vault — every chunk, every manifest — is encrypted under this
  one key.
- **WrappedVaultKey**: the only form the vault key is ever persisted in.
  A passphrase is run through Argon2id (m=19456 KiB, t=2, p=1 — the same
  parameters `backend/internal/identity/password.go` uses for password
  hashing, so the project has one consistent story about its KDF choice,
  not two) to derive a key-wrapping key, which AES-256-GCM-encrypts the
  vault key. Stored in the local device state file (`DeviceState` in
  `backup-cli/src/state.rs`) — never sent to the server.
- **Chunks and the manifest**: AES-256-GCM, random 96-bit nonce per
  encryption, with associated data (AAD) that is authenticated but not
  secret:
  - A chunk's AAD is its own plaintext content hash — binds a specific
    ciphertext to a specific chunk identity, so object storage (or a
    compromised server) cannot swap one chunk's bytes onto another
    chunk's metadata without the swap being detected on decrypt.
  - A manifest's AAD is the fixed string `"deeparchive-manifest-v1"` —
    binds ciphertext to "this is a manifest," not interchangeable with a
    chunk even though both are, to anyone without the key, just AES-GCM
    ciphertext.

## Content hashing and the dedup leak (accepted tradeoff)

`crypto::content_hash` is SHA-256 of the **plaintext**. It is used for
two things: the chunker's dedup key, and the chunk's encryption AAD. This
means:

- Two chunks with identical plaintext always produce the same hash, so
  the server can recognize "already have this" and skip storing it
  twice — that's the point.
- It also means the server can tell when two chunks (within the same
  dedup scope) have identical content, even though it cannot see what
  that content is. A content-equality oracle is a real, accepted leak,
  not an oversight.

The leak is scoped, not global: `backend/db/migrations/0004_backups.sql`
deduplicates chunks **per organization if the uploading device belongs
to one, otherwise per individual user** — never across unrelated tenants.
A different user (or a different, unrelated organization) uploading the
exact same bytes always gets `exists: false` from `/negotiate`; see
`TestBackups_DedupIsScopedPerUser_NotGlobal` in
`backend/tests/integration/backups_test.go`, which asserts this directly
against a real database.

## Known limitations (do not assume otherwise)

- **Nonce collision bound.** Chunk nonces are random, not a counter. NIST's
  guidance for 96-bit GCM nonces generated randomly puts a birthday-bound
  collision risk around 2^32 encryptions under one key. A personal backup
  vault is extremely unlikely to reach that volume, but vault key rotation
  (re-encrypting everything under a fresh key) is not implemented and
  should be before this is used at a scale where it could matter.
- **No multi-device vault sharing.** Each `backup-cli init` generates a
  brand new, unrelated `VaultKey`. Two devices for the same user today
  have two unconnected vaults — no cross-device dedup, and a device
  cannot restore a snapshot another device committed unless it is handed
  that other device's wrapped vault key and passphrase out of band. This
  is a real scope limitation of the current client, not a server-side
  restriction.
- **No server-side key escrow, by design.** Losing both the passphrase
  and the local device state file (which holds the wrapped vault key)
  makes every snapshot encrypted under that vault permanently
  undecryptable. There is no "forgot my passphrase" recovery path, and
  adding one would mean the server could decrypt backups, which defeats
  the point.
- **The device's Ed25519 private key is not itself encrypted at rest** —
  `DeviceState` stores it in a file with `0600` permissions (Unix), the
  same protection model as an SSH private key, not a passphrase-wrapped
  secret like the vault key.
- **No whole-file streaming.** `backup-core`'s chunker takes a file fully
  loaded into memory (`&[u8]`); `backup-cli` reads each file into memory
  before chunking it. Fine for personal documents and photos; not yet
  appropriate for files much larger than available RAM (e.g. VM disk
  images) — see `ChunkerConfig::default`'s doc comment.
