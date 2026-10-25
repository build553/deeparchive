# deepctl — the end-user CLI

`deepctl` is the command-line client for a normal DeepArchive account. It
enrolls a machine, backs up a directory, lists your snapshots, and restores
one. It talks only to the HTTP API (the same one the web dashboard uses) and
needs no database access.

If you operate the platform itself (all accounts, quotas, retention), see
[deepctl-admin.md](deepctl-admin.md) instead. The two are separate binaries on
purpose: a *platform admin* can act on every account, while an *organization
admin* is just a role inside one organization and uses `deepctl` like any
other member.

## Install

Release builds for Linux, macOS, and Windows are attached to each GitHub
release (`deepctl-<version>-<os>-<arch>.tar.gz` / `.zip`). To build from
source:

```
cd backend
go build -o bin/deepctl ./cmd/deepctl
```

## Quick start

```
deepctl login      # once per machine; opens your browser, nothing to type
deepctl init       # once per machine; asks for a vault passphrase
deepctl backup ~/Documents
deepctl list
deepctl restore --to ~/restored     # restores your most recent snapshot
```

There is nothing to configure: no server URL, no file paths. `login` remembers
which server you signed in to, and state lives in your user config directory,
so every command works from any directory.

## Where things are stored

| File | Location | Written by |
| --- | --- | --- |
| Signed-in session + server | `credentials.json` | `login` |
| Device identity + wrapped vault key | `device.json` | `init` |

Both live in `deeparchive/` under your OS config directory
(`~/Library/Application Support` on macOS, `~/.config` on Linux, `%AppData%`
on Windows) with mode `0600`. Treat them like an SSH private key: anyone who
can read `device.json` can impersonate that device, and anyone who can read
`credentials.json` is signed in as you. **Back up `device.json`** — together
with your passphrase it is the way to read your backups with `deepctl`.

## Which server

In order of precedence: the `--server` flag, the `DEEPARCHIVE_SERVER`
environment variable, the server remembered by `login`, then the default baked
into the binary (the hosted service for release builds; `http://localhost:8080`
for a plain `go build`). You normally never set any of these.

## Passphrases

`init`, `backup`, and `restore` ask for your vault passphrase on the terminal
with echo off (and `init` asks twice). For scripts, set `DEEPARCHIVE_PASSPHRASE`
or pass `--passphrase`, but prefer the prompt or the environment variable: a
flag value is visible in shell history and the process list.

## Global flags (all optional)

| Flag | Default | Purpose |
| --- | --- | --- |
| `--server` | see above | Override the API base URL. |
| `--state` | `device.json` in the config dir | Device identity file. |
| `--credentials` | `credentials.json` in the config dir | Signed-in session file. |
| `--resolve HOST:IP:PORT` | automatic | Repeatable DNS override. Applied for you when the server is on localhost (see below). |

## Commands

### `deepctl login`

Signs you in through your browser using the device-code flow, the same shape
as `gh auth login`.

1. `deepctl` prints a short one-time code (for example `WDJB-MJHT`) and a URL.
2. It tries to open the URL in your browser. If that fails, open it yourself.
3. In the browser, sign in if you are not already, check that the code matches
   your terminal, and click **Approve**.
4. `deepctl` is polling in the meantime; within a few seconds it receives a
   session and saves it to `--credentials`.

No password is typed into the terminal or stored anywhere. Codes expire after
15 minutes and work once. If you did not start the login yourself, click
**Deny** in the browser.

Optional flag: `--client-name` (default `deepctl`) is the name shown on the approval
page.

Later commands use the stored sign-in automatically. They refresh it on every
run, so you should not need to log in again unless you run `logout` or revoke
the session from the web dashboard's Account page.

### `deepctl logout`

Deletes `--credentials`. This is local only; to revoke the session on the
server as well, use **Active sessions** on the Account page.

### `deepctl init`

Enrolls this machine and creates its encryption vault. Requires `login` first.

```
deepctl init            # or: deepctl init --name laptop
```

| Flag | Required | Purpose |
| --- | --- | --- |
| `--name` | no | Label shown in the dashboard. Default: this computer's hostname. |
| `--passphrase` | no | Protects the vault key. Default: asked on the terminal. |
| `--os` | no | OS label. Default: detected. |

It generates an Ed25519 device keypair, enrolls and self-approves the device,
generates a fresh vault key, wraps it under your passphrase (Argon2id), and
writes everything to `device.json`. It refuses to run if that file already exists.

The wrapped (already encrypted) vault key is also saved to your account, so the
web dashboard can open your files when you type only your passphrase, with no
`device.json` upload. The server never sees the passphrase or the unlocked key:
the browser checks the passphrase itself by decrypting the key.

**Your passphrase never leaves your machine and there is no recovery
without it.** Lose the passphrase and your backups are permanently unreadable,
by design. Choose a strong passphrase: anyone who obtained the stored wrapped
key could try to guess it offline, and only the passphrase's strength (plus
Argon2id's cost) stands in the way.

### `deepctl backup <directory>`

Scans the directory, splits files into content-defined chunks, encrypts each
chunk on your machine, uploads only chunks the server does not already have,
and commits an encrypted snapshot.

```
deepctl backup ~/Documents --exclude '*.tmp' --exclude 'node_modules/'
```

| Flag | Purpose |
| --- | --- |
| `--passphrase` | Optional. Unlocks the vault key; default: asked on the terminal. |
| `--exclude` | Repeatable exclude pattern. `name/` skips any directory with that name, a pattern with no `/` matches file names (`*.tmp`), and anything else matches the relative path. |

Behavior worth knowing:

- Symlinks are never followed.
- File names, sizes, and the chunk map are encrypted in the manifest; the
  server sees only opaque chunk hashes and sizes.
- A repeat backup of unchanged data uploads nothing
  (`uploaded 0 new chunks (N already present)`).
- Each file is read fully into memory while chunking, so files much larger
  than available RAM are not supported yet.
- Backup authenticates as the *device* (signing a server challenge with the
  key in `--state`), not as you, so it does not need `login`.

### `deepctl list`

Prints every snapshot visible to you: ID, creation time, total size, and chunk
count. Requires `login`.

### `deepctl restore`

Downloads a snapshot, decrypts it, and rebuilds the files.

```
deepctl restore --to ~/restored                  # most recent snapshot
deepctl restore --to ~/restored --snapshot <id>  # a specific one
```

Only `--to` is required. Without `--snapshot` the most recent one is restored;
get other IDs from `deepctl list`. Requires `login` and the `device.json` from
the device whose vault encrypted the snapshot.

Restore refuses manifest paths that are absolute or contain `..`, so a corrupt
manifest cannot write outside the `--to` directory. Unix permission bits are
restored; on Windows only the read-only bit is applied.

## Local development

Against the Docker Compose stack, upload and download URLs name the
compose-internal host `minio`, which only containers can resolve. When the
server is on localhost, `deepctl` routes `minio:9000` to `127.0.0.1:9000`
automatically. For any other layout, pass `--resolve minio:<ip>:9000`
(format `HOST:IP:PORT`; only the host is rewritten).

## Exit codes and errors

`deepctl` exits 0 on success and 1 on any error, printing `error: ...` to
stderr. Common messages:

| Message | Meaning |
| --- | --- |
| `not logged in — run deepctl login first` | No saved sign-in; run `login`. |
| `stored sign-in is no longer valid` | Session expired or revoked; run `login` again. |
| `unwrapping vault key (wrong passphrase?)` | Wrong passphrase for this `device.json`. |
| `... already exists — remove it first if you really want to re-enroll` | `init` was already run here. |
| `code expired before it was approved` | You waited over 15 minutes; run `login` again. |
| `sign-in was denied` | Someone clicked **Deny** in the browser. |

## Security notes

- `login` uses a device-authorization flow modeled on RFC 8628. The long
  `device_code` stays on your machine; only the short user code is typed or
  shown. The server stores only a hash of the device code, and a redeemed code
  cannot be reused.
- All encryption is AES-256-GCM on your machine, with the chunk's content hash
  bound in as associated data. See [encryption.md](encryption.md) for the full
  threat model, including what content-hash deduplication reveals to the
  server.
- The wire format matches the Rust engine in `crates/backup-core`, which the
  desktop app embeds, so a snapshot made by either client restores on the
  other. Chunk boundaries may differ between them; that is harmless because
  each snapshot records its own boundaries.
