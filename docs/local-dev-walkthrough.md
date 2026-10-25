# Local dev walkthrough

A click-through (and run-through) guide to the whole stack running on
your own machine: the web dashboard, the backup CLI, and the Dev Inbox
that stands in for a real email provider. Everything below runs against
your own local Postgres/MinIO — no third-party service, no real email,
nothing leaves your machine.

## 0. Start everything

```bash
cp .env.example .env   # if you haven't already
docker compose up -d --build
```

This brings up Postgres, MinIO, the API, the worker, and the web
dashboard together. Give it a minute, then confirm:

```bash
curl http://localhost:8080/healthz   # {"status":"ok"}
```

Open **http://localhost:5173** — that's the web dashboard.

## 1. Seed some dummy accounts

```bash
make seed
```

Creates three accounts, already verified, all with password
`DevPassword123!`:

| Email | Role in "Acme Dev Org" |
|---|---|
| `alice@example.test` | owner |
| `bob@example.test` | admin |
| `carol@example.test` | member |

Safe to run again — it skips anything that already exists. **Never run
this against a production database** — `cmd/seed` refuses to if
`APP_ENV=production`, but treat it as dev-only regardless.

Log in as `alice@example.test` at http://localhost:5173/login and look
around: Devices, Backups, Organizations (you should see "Acme Dev Org"
with bob/carol as members), Storage, Security, Account. All of it is
real — every button calls the real API against your local database.

## 2. The Dev Inbox: register a brand-new account for real

The seed accounts skip email verification entirely (seed data should be
immediately usable). To see the *real* flow — the one an actual new
signup goes through — register a new account instead:

1. Go to http://localhost:5173/register, sign up with any email (e.g.
   `you@example.test`) and a password.
2. You'll land on "Check your email." Click **Dev: view sent emails**
   (or go straight to http://localhost:5173/dev/inbox).
3. The verification email is sitting there — this page polls every few
   seconds, so if you don't see it yet, wait a moment. Click **Verify
   email** right on the card. No real email provider, no copy-pasting a
   token by hand (though the raw token is shown too, if you want it).
4. Log in with the account you just registered.

This same Dev Inbox page is where **password reset** and **organization
invitation** emails show up too — try "Forgot password" on the login
page and watch the reset email appear, with an inline "new password"
field right on the card.

The Dev Inbox only exists when the API is started with
`APP_ENV=development` (true by default in `.env.example`) — see
`docs/implementation-status.md` for why it's safe to leave in a dev
build but must never ship to production.

## 3. Create a real backup

The dashboard manages devices and backups but never touches plaintext
or the vault passphrase — that's `backup-cli`'s job (Rust). Build it
once:

```bash
cd crates
cargo build -p backup-cli
cd ..
```

(A debug build is fine here and avoids needing an optimized build just
to test locally — this is `backup-cli`, not something to ship.)

Enroll a device for one of your accounts and back up a real directory.
Running from the host (not inside Docker), `--resolve` makes the
presigned MinIO URLs reachable — see `docs/backup-protocol.md` for why:

```bash
./crates/target/debug/backup-cli \
  --server http://localhost:8080 \
  --state ./deeparchive-device.json \
  --resolve minio:127.0.0.1:9000 \
  init --email alice@example.test --password DevPassword123! \
       --name "My Laptop" --os macos --passphrase "a vault passphrase, not your account password"

./crates/target/debug/backup-cli \
  --server http://localhost:8080 \
  --state ./deeparchive-device.json \
  --resolve minio:127.0.0.1:9000 \
  backup ~/Documents/some-folder --passphrase "a vault passphrase, not your account password"
```

Now in the dashboard: **Devices** shows "My Laptop"; **Backups** shows
the new snapshot. Click into it — you'll see size/chunk-count metadata
and the exact `backup-cli restore` command to run (the dashboard can't
decrypt it for you; only the Rust client has the crypto, and only you
have the passphrase).

Try restoring it for real:

```bash
./crates/target/debug/backup-cli \
  --server http://localhost:8080 \
  --state ./deeparchive-device.json \
  --resolve minio:127.0.0.1:9000 \
  restore --email alice@example.test --password DevPassword123! \
          --passphrase "a vault passphrase, not your account password" \
          --snapshot <snapshot-id-from-the-dashboard> --to ./restored
diff -r ~/Documents/some-folder ./restored   # should be silent
```

## 4. Delete the backup

Back in the dashboard, on the Backups page, click **Delete** on the
snapshot and confirm. This isn't a soft delete: the underlying chunk
objects are actually removed from MinIO if nothing else references them
— see `docs/implementation-status.md`'s Milestone 4 section. You can
confirm it's really gone via the MinIO console at
http://localhost:9001 (login with `S3_ACCESS_KEY_ID`/
`S3_SECRET_ACCESS_KEY` from your `.env`).

## 5. The organization workflow

The seed script added bob/carol to "Acme Dev Org" directly, skipping
the invite ceremony — try the real thing:

1. Log in as `alice@example.test`, go to **Organizations → Acme Dev
   Org**, and invite a new email address (e.g. `dave@example.test`) as
   a member.
2. Go to the Dev Inbox — the invitation email is there. Click **Copy
   invitation token**.
3. Register `dave@example.test` as a new account (and verify it via the
   Dev Inbox, same as step 2 above).
4. Log in as dave, go to **Organizations**, paste the token into
   "Accept an invitation."

## 6. Deactivating an account

**Account** page → "Deactivate account" (requires your current
password). This is deliberately a soft delete — it blocks login and
revokes every session, but doesn't erase your data. See
`docs/implementation-status.md`'s Milestone 2 section for why.

## Cleaning up

```bash
docker compose down -v   # deletes all local dev data — Postgres and MinIO volumes
rm deeparchive-device.json
```
