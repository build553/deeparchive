# deepctl-admin — the platform-operator CLI

`deepctl-admin` manages every account on a DeepArchive deployment: list and
inspect users and organizations, set storage quotas and retention limits,
suspend or reactivate accounts, change platform roles, and trigger a retention
sweep.

**This tool is for platform operators only.** It connects directly to the
database and object storage, with no per-command authorization check, because
whoever can run it already holds `DATABASE_URL` and can read the database
directly. The security boundary is access to that configuration, not a login.
Never distribute it to end users or give it to an organization admin; they
want [`deepctl`](deepctl.md).

A note on the two kinds of "admin":

| | Platform admin | Organization admin |
| --- | --- | --- |
| Scope | Every account on the deployment | One organization |
| How it is held | `users.platform_role = 'admin'` (and this tool's database access) | A membership role inside an org |
| Tools | `deepctl-admin`, and the web Admin panel | `deepctl` and the normal dashboard, like any member |

The web Admin panel (`/admin`, platform admins only, with an emailed one-time
code at sign-in) covers most of the same operations. This CLI is for
automation, for bootstrapping the first admin, and for when the web stack is
down.

## Install

Release builds are attached to each GitHub release
(`deepctl-admin-<version>-<os>-<arch>.tar.gz` / `.zip`). From source:

```
cd backend
go build -o bin/deepctl-admin ./cmd/deepctl-admin
```

## Configuration

`deepctl-admin` reads the same environment variables as the API and worker, so
run it with the same `.env`:

| Variable | Required | Used for |
| --- | --- | --- |
| `DATABASE_URL` | yes | Every command. |
| `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` | yes | Client construction; `retention sweep` deletes objects. |
| `ACCESS_TOKEN_SIGNING_KEY` | production | Required by config validation in production. |

Against the local Compose stack, the database is published on `localhost:5432`
and MinIO on `localhost:9000`, so:

```
set -a; source .env; set +a
./bin/deepctl-admin users list
```

`help` and `version` run without any configuration. Every other command loads
configuration, connects to Postgres, and gives up after 30 seconds in total.

## Commands

All output is plain text; list commands print aligned columns. Failures print
`error: ...` to stderr and exit 1; a bad command shows its usage line.

### Users

```
deepctl-admin users list [--limit N] [--offset N]
deepctl-admin users show <email>
deepctl-admin users set-quota <email> <bytes>
deepctl-admin users set-retention <email> <keep>
deepctl-admin users suspend <email>
deepctl-admin users activate <email>
deepctl-admin users deactivate <email>
deepctl-admin users set-role <email> <none|operator|admin>
```

- **`list`** prints email, status, platform role, quota (bytes), retention, and
  creation date. Defaults to `--limit 50 --offset 0`.
- **`show`** prints one account's full record: ID, display name, status,
  platform role, quota, retention, creation time.
- **`set-quota`** sets the account's personal storage quota in bytes. Existing
  data is not deleted if usage already exceeds it; new backups that would
  exceed the quota are rejected.
- **`set-retention`** sets how many snapshots to keep per device. The limit is
  applied by the next retention sweep (the worker runs one on an interval, or
  run `retention sweep`).
- **`suspend`**, **`activate`**, **`deactivate`** set the account status. A
  suspended or deactivated account cannot log in. Use `suspend` for a
  reversible hold and `deactivate` for an account that should stay closed;
  both can be reversed with `activate`. Backups are never deleted by a status
  change.
- **`set-role`** sets the platform-wide role. Use it to promote the very first
  admin (the web panel requires already being one):

  ```
  deepctl-admin users set-role you@example.com admin
  ```

  Once an account is `admin`, it signs in to the web dashboard with a password
  plus a one-time code emailed to it. `operator` and `none` have no admin
  access today.

### Organizations

```
deepctl-admin orgs list [--limit N] [--offset N]
deepctl-admin orgs show <org-id>
deepctl-admin orgs set-quota <org-id> <bytes>
deepctl-admin orgs set-retention <org-id> <keep>
```

Organizations are addressed by ID (get it from `orgs list`). These mirror the
user commands, for the organization's shared storage quota and retention.
`orgs show` prints the ID, name, slug, owner ID, quota, retention, and creation
time. Use the web Admin panel to see an organization's members.

### Retention

```
deepctl-admin retention sweep
```

Enforces every account's and organization's retention limit now, deleting
snapshots beyond the limit and any chunks no longer referenced, then prints
`retention sweep complete: N snapshot(s) deleted`. The worker already does
this on a timer (`RETENTION_SWEEP_INTERVAL`, default one hour); run it by hand
after lowering a limit when you do not want to wait. **Deleted snapshots
cannot be recovered.**

### Usage

```
deepctl-admin usage <email>
```

Prints used and quota bytes for the account's personal storage and for each
organization it belongs to.

### Version

```
deepctl-admin version
```

## Safety notes

- There is no confirmation prompt and no dry-run. Commands act immediately;
  `retention sweep` is the only destructive one.
- Platform-role changes made here are recorded in the audit log under the
  account itself (`admin.user_platform_role_set`), because the CLI has no
  signed-in actor. The web panel records the acting admin and blocks changing
  your own role; this CLI does not, since its caller already has database
  access.
- Keep the environment file that holds `DATABASE_URL` and the S3 keys as
  tightly controlled as the database itself.
