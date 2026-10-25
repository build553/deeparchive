//! End-to-end backup client: authenticates as a device (Ed25519
//! challenge/response, like `backup heartbeat`) or as a user (email +
//! password, for enrollment and restore, which the API gates by user
//! session — see `backend/internal/backups/handlers.go`'s `Mount` doc
//! comment for why those two routes use different identities), scans and
//! encrypts a directory via `backup-core`, uploads only the chunks the
//! server doesn't already have, commits a snapshot, and can restore one
//! back to a directory with byte-for-byte matching content.

mod api;
mod state;

use std::collections::{BTreeMap, HashMap};
use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::thread;
use std::time::Duration;

use anyhow::{bail, Context, Result};
use backup_core::crypto::{self, WrappedVaultKey};
use backup_core::manifest::{ChunkMeta, EncryptedManifest, FileEntry};
use backup_core::{chunker, scanner, ChunkerConfig, Manifest, VaultKey};
use base64::engine::general_purpose::STANDARD as base64;
use base64::Engine;
use clap::{Parser, Subcommand};
use ed25519_dalek::{Signer, SigningKey};

use crate::api::{Api, CommitChunk, CommitRequest, DevicePoll};
use crate::state::{DeviceState, UserCredentials};

#[derive(Parser)]
#[command(name = "backup-cli", about = "DeepArchive end-to-end backup client")]
struct Cli {
    /// Base URL of the DeepArchive API, e.g. http://localhost:8080
    #[arg(long, env = "DEEPARCHIVE_SERVER")]
    server: String,

    /// Path to the local device state file written by `init`.
    #[arg(long, default_value = "deeparchive-device.json")]
    state: PathBuf,

    /// Path to the local sign-in state file written by `login`.
    #[arg(long, default_value = "deeparchive-credentials.json")]
    credentials: PathBuf,

    /// Override DNS resolution for HOST:IP:PORT (repeatable), exactly like
    /// curl's --resolve. Needed for local Docker Compose development only:
    /// presigned object-storage URLs embed the compose-internal hostname
    /// "minio", which only the API container can resolve by default — pass
    /// e.g. --resolve minio:127.0.0.1:9000 to run this client from the host.
    #[arg(long = "resolve", value_name = "HOST:IP:PORT")]
    resolve: Vec<String>,

    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// Sign in via your browser — the same device-code flow `gh auth
    /// login` uses. No password is ever typed into this terminal or
    /// saved anywhere; only an access/refresh token pair is, in
    /// --credentials. Run this once per machine, then omit
    /// --email/--password on every other command.
    Login {
        /// Shown on the approval page ("<name> wants to sign in").
        #[arg(long, default_value = "backup-cli")]
        client_name: String,
    },
    /// Forget the locally stored sign-in (deletes --credentials).
    Logout,
    /// Register a new device and vault: signs in (via stored login, or
    /// --email/--password), generates a device keypair, enrolls and
    /// self-approves it, generates a fresh vault key, and wraps it under
    /// --passphrase. Writes --state.
    Init {
        #[arg(long)]
        email: Option<String>,
        #[arg(long)]
        password: Option<String>,
        #[arg(long)]
        name: String,
        #[arg(long, default_value = "unknown")]
        os: String,
        #[arg(long)]
        passphrase: String,
    },
    /// Scan, chunk, encrypt, and back up a directory.
    Backup {
        directory: PathBuf,
        #[arg(long)]
        passphrase: String,
        /// Gitignore-style exclude pattern (repeatable), e.g. --exclude '*.tmp'.
        #[arg(long = "exclude")]
        exclude: Vec<String>,
    },
    /// List snapshots visible to the logged-in user.
    List {
        #[arg(long)]
        email: Option<String>,
        #[arg(long)]
        password: Option<String>,
    },
    /// Restore a snapshot's files into a target directory.
    Restore {
        #[arg(long)]
        email: Option<String>,
        #[arg(long)]
        password: Option<String>,
        #[arg(long)]
        passphrase: String,
        #[arg(long)]
        snapshot: String,
        #[arg(long)]
        to: PathBuf,
    },
}

fn main() -> Result<()> {
    let cli = Cli::parse();

    let mut resolves = Vec::new();
    for spec in &cli.resolve {
        resolves.push(api::parse_resolve(spec)?);
    }
    let api = Api::new(&cli.server, &resolves)?;

    match cli.command {
        Command::Login { client_name } => login(&api, &cli.credentials, &client_name),
        Command::Logout => logout(&cli.credentials),
        Command::Init {
            email,
            password,
            name,
            os,
            passphrase,
        } => init(
            &api,
            &cli.state,
            &cli.credentials,
            email.as_deref(),
            password.as_deref(),
            &name,
            &os,
            &passphrase,
        ),
        Command::Backup {
            directory,
            passphrase,
            exclude,
        } => backup(&api, &cli.state, &directory, &passphrase, &exclude),
        Command::List { email, password } => list(
            &api,
            &cli.credentials,
            email.as_deref(),
            password.as_deref(),
        ),
        Command::Restore {
            email,
            password,
            passphrase,
            snapshot,
            to,
        } => restore(
            &api,
            &cli.state,
            &cli.credentials,
            email.as_deref(),
            password.as_deref(),
            &passphrase,
            &snapshot,
            &to,
        ),
    }
}

/// Resolves a user access token: --email/--password if both given (for
/// scripts/CI, unchanged from before this command existed), otherwise the
/// stored login from `backup-cli login`. Always refreshes the stored
/// session rather than checking its expiry client-side — see
/// Api::refresh's doc comment for why — and persists the rotated tokens
/// before returning.
fn resolve_user_token(
    api: &Api,
    creds_path: &Path,
    email: Option<&str>,
    password: Option<&str>,
) -> Result<String> {
    match (email, password) {
        (Some(e), Some(p)) => Ok(api.login(e, p)?.access_token),
        (None, None) => {
            let creds = UserCredentials::load(creds_path).context(
                "not logged in — run `backup-cli login`, or pass --email/--password",
            )?;
            let session = api
                .refresh(&creds.refresh_token)
                .context("stored sign-in is no longer valid — run `backup-cli login` again")?;
            let new_creds = UserCredentials {
                access_token: session.access_token.clone(),
                refresh_token: session.refresh_token,
            };
            new_creds.save(creds_path)?;
            Ok(session.access_token)
        }
        _ => bail!("--email and --password must be given together"),
    }
}

/// The device-code flow: print a short code and a URL, try to open the
/// URL in the person's browser, then poll until they approve it there —
/// identical shape to `gh auth login`.
fn login(api: &Api, creds_path: &Path, client_name: &str) -> Result<()> {
    let start = api.device_code(client_name).context("starting sign-in")?;

    println!("First copy your one-time code: {}", start.user_code);
    println!();
    println!(
        "Then open this URL in your browser to confirm:\n  {}",
        start.verification_uri_complete
    );
    let _ = open_browser(&start.verification_uri_complete);
    println!();
    print!("Waiting for confirmation in the browser...");
    std::io::stdout().flush().ok();

    let mut interval = Duration::from_secs(start.interval.max(1));
    loop {
        thread::sleep(interval);
        match api.device_token(&start.device_code)? {
            DevicePoll::Pending => {
                print!(".");
                std::io::stdout().flush().ok();
            }
            DevicePoll::SlowDown => {
                interval += Duration::from_secs(5);
            }
            DevicePoll::Denied => {
                println!();
                bail!("sign-in was denied");
            }
            DevicePoll::Expired => {
                println!();
                bail!("code expired before it was approved — run `backup-cli login` again");
            }
            DevicePoll::Approved(session) => {
                println!();
                let creds = UserCredentials {
                    access_token: session.access_token,
                    refresh_token: session.refresh_token,
                };
                creds.save(creds_path)?;
                println!("logged in — credentials saved to {}", creds_path.display());
                return Ok(());
            }
        }
    }
}

fn logout(creds_path: &Path) -> Result<()> {
    if creds_path.exists() {
        fs::remove_file(creds_path)
            .with_context(|| format!("removing {}", creds_path.display()))?;
        println!("logged out");
    } else {
        println!("already logged out");
    }
    Ok(())
}

/// Best-effort: a browser that doesn't open is not a failure, just a
/// nudge to copy the URL that's already been printed.
fn open_browser(url: &str) -> std::io::Result<std::process::Child> {
    #[cfg(target_os = "macos")]
    {
        std::process::Command::new("open").arg(url).spawn()
    }
    #[cfg(target_os = "linux")]
    {
        std::process::Command::new("xdg-open").arg(url).spawn()
    }
    #[cfg(target_os = "windows")]
    {
        std::process::Command::new("cmd")
            .args(["/C", "start", "", url])
            .spawn()
    }
    #[cfg(not(any(target_os = "macos", target_os = "linux", target_os = "windows")))]
    {
        Err(std::io::Error::new(
            std::io::ErrorKind::Unsupported,
            "no known way to open a browser on this platform",
        ))
    }
}

fn init(
    api: &Api,
    state_path: &Path,
    creds_path: &Path,
    email: Option<&str>,
    password: Option<&str>,
    name: &str,
    os: &str,
    passphrase: &str,
) -> Result<()> {
    if state_path.exists() {
        bail!(
            "{} already exists — remove it first if you really want to re-enroll",
            state_path.display()
        );
    }

    let user_token = resolve_user_token(api, creds_path, email, password)?;

    let mut seed = [0u8; 32];
    getrandom::fill(&mut seed).expect("reading OS randomness cannot fail on a supported platform");
    let signing_key = SigningKey::from_bytes(&seed);
    let public_key_b64 = base64.encode(signing_key.verifying_key().to_bytes());

    let device_id = api
        .enroll_device(
            &user_token,
            name,
            os,
            env!("CARGO_PKG_VERSION"),
            &public_key_b64,
        )
        .context("enrolling device")?;
    api.approve_device(&user_token, &device_id)
        .context("approving device")?;

    let vault_key = VaultKey::generate();
    let wrapped = WrappedVaultKey::wrap(&vault_key, passphrase).context("wrapping vault key")?;

    let state = DeviceState::new(device_id.clone(), &signing_key, wrapped);
    state.save(state_path)?;

    println!("enrolled device {device_id}");
    println!("state written to {}", state_path.display());
    println!("remember your passphrase — it is never sent to the server and there is no recovery without it");
    Ok(())
}

/// Authenticates as the device recorded in `state`: issues a challenge,
/// signs it with the device's private key, and exchanges the signature for
/// a short-lived device access token. Mirrors exactly what the Go API's
/// `devices.Service.Authenticate` verifies — see that package's doc
/// comments.
fn authenticate_device(api: &Api, state: &DeviceState) -> Result<String> {
    let signing_key = state.signing_key()?;
    let (challenge_id, nonce_b64) = api.issue_challenge(&state.device_id)?;
    let nonce = base64
        .decode(&nonce_b64)
        .context("decoding challenge nonce")?;
    let signature = signing_key.sign(&nonce);
    let signature_b64 = base64.encode(signature.to_bytes());
    api.authenticate_device(&state.device_id, &challenge_id, &signature_b64)
}

fn backup(
    api: &Api,
    state_path: &Path,
    directory: &Path,
    passphrase: &str,
    exclude: &[String],
) -> Result<()> {
    let state = DeviceState::load(state_path)?;
    let vault_key = state
        .wrapped_vault_key
        .unwrap(passphrase)
        .context("unwrapping vault key (wrong passphrase?)")?;

    let scanned = scanner::scan(directory, exclude).context("scanning directory")?;
    println!("scanned {} files", scanned.len());

    let config = ChunkerConfig::default();
    let mut file_entries = Vec::with_capacity(scanned.len());
    // Deduplicated across the whole run, not just within one file — pack()
    // only dedups within a single call, so this merge step is what makes
    // identical content shared between two files in the same backup not
    // get encrypted (or counted) twice.
    let mut chunk_meta: BTreeMap<String, ChunkMeta> = BTreeMap::new();
    let mut chunk_plaintext_free: HashMap<String, Vec<u8>> = HashMap::new(); // hash -> ciphertext

    for file in &scanned {
        let data = fs::read(&file.absolute_path)
            .with_context(|| format!("reading {}", file.absolute_path.display()))?;
        let packed = chunker::pack(&data, &vault_key, &config);
        for c in packed.chunks {
            chunk_meta
                .entry(c.hash.clone())
                .or_insert_with(|| ChunkMeta {
                    nonce: hex::encode(c.nonce),
                    ciphertext_size: c.ciphertext.len() as u64,
                });
            chunk_plaintext_free
                .entry(c.hash.clone())
                .or_insert(c.ciphertext);
        }
        file_entries.push(FileEntry {
            path: file.relative_path.clone(),
            size: file.size,
            mode: file.mode,
            modified: file.modified.clone(),
            chunks: packed.chunk_refs,
        });
    }

    let manifest = Manifest::new(file_entries, chunk_meta);
    let hashes: Vec<String> = manifest
        .referenced_chunk_hashes()
        .iter()
        .map(|h| h.to_string())
        .collect();
    println!(
        "{} distinct chunks across {} files ({} total bytes)",
        hashes.len(),
        manifest.files.len(),
        manifest.total_size
    );

    let device_token = authenticate_device(api, &state).context("authenticating device")?;

    let statuses = api
        .negotiate(&device_token, &state.device_id, &hashes)
        .context("negotiating upload")?;
    let mut uploaded = 0;
    for status in &statuses {
        let Some(upload_url) = &status.upload_url else {
            debug_assert!(
                status.exists,
                "no upload_url implies the server already has this chunk"
            );
            continue; // server already has this chunk — nothing to upload
        };
        let ciphertext = chunk_plaintext_free.get(&status.hash).ok_or_else(|| {
            anyhow::anyhow!("server asked for chunk {} we never produced", status.hash)
        })?;
        api.put_bytes(upload_url, ciphertext.clone())
            .with_context(|| format!("uploading chunk {}", status.hash))?;
        uploaded += 1;
    }
    println!(
        "uploaded {uploaded} new chunks ({} already present)",
        hashes.len() - uploaded
    );

    let encrypted_manifest = manifest
        .encrypt(&vault_key)
        .context("encrypting manifest")?;
    let commit_chunks = hashes
        .iter()
        .map(|h| CommitChunk {
            hash: h.clone(),
            ciphertext_size: chunk_plaintext_free[h].len() as u64,
        })
        .collect();
    let snapshot_id = api
        .commit_snapshot(
            &device_token,
            &state.device_id,
            &CommitRequest {
                manifest_nonce: base64.encode(encrypted_manifest.nonce),
                manifest_ciphertext: base64.encode(&encrypted_manifest.ciphertext),
                total_size: manifest.total_size,
                chunks: commit_chunks,
            },
        )
        .context("committing snapshot")?;

    println!("snapshot committed: {snapshot_id}");
    Ok(())
}

fn list(api: &Api, creds_path: &Path, email: Option<&str>, password: Option<&str>) -> Result<()> {
    let user_token = resolve_user_token(api, creds_path, email, password)?;
    let snapshots = api.list_backups(&user_token)?;
    if snapshots.is_empty() {
        println!("no snapshots yet");
    }
    for s in snapshots {
        println!(
            "{}  {}  {} bytes, {} chunks",
            s.id, s.created_at, s.total_size, s.chunk_count
        );
    }
    Ok(())
}

fn restore(
    api: &Api,
    state_path: &Path,
    creds_path: &Path,
    email: Option<&str>,
    password: Option<&str>,
    passphrase: &str,
    snapshot_id: &str,
    to: &Path,
) -> Result<()> {
    let state = DeviceState::load(state_path)?;
    let vault_key = state
        .wrapped_vault_key
        .unwrap(passphrase)
        .context("unwrapping vault key (wrong passphrase?)")?;

    let user_token = resolve_user_token(api, creds_path, email, password)?;
    let detail = api.get_snapshot(&user_token, snapshot_id)?;

    let manifest_nonce: [u8; crypto::NONCE_LEN] = base64
        .decode(&detail.manifest_nonce)
        .context("decoding manifest nonce")?
        .try_into()
        .map_err(|_| anyhow::anyhow!("manifest nonce from server has the wrong length"))?;
    let manifest_ciphertext = base64
        .decode(&detail.manifest_ciphertext)
        .context("decoding manifest ciphertext")?;
    let manifest = EncryptedManifest {
        nonce: manifest_nonce,
        ciphertext: manifest_ciphertext,
    }
    .decrypt(&vault_key)
    .context("decrypting manifest (wrong passphrase, or this vault didn't create this snapshot)")?;

    println!(
        "restoring {} files ({} bytes) into {}",
        manifest.files.len(),
        manifest.total_size,
        to.display()
    );

    let mut chunk_plaintexts: HashMap<String, Vec<u8>> =
        HashMap::with_capacity(detail.chunks.len());
    for loc in &detail.chunks {
        let meta = manifest.chunks.get(&loc.hash).ok_or_else(|| {
            anyhow::anyhow!(
                "server listed chunk {} that the manifest never references",
                loc.hash
            )
        })?;
        let nonce: [u8; crypto::NONCE_LEN] = hex::decode(&meta.nonce)
            .with_context(|| format!("chunk {} has invalid nonce encoding", loc.hash))?
            .try_into()
            .map_err(|_| anyhow::anyhow!("chunk {} nonce has the wrong length", loc.hash))?;
        let ciphertext = api
            .get_bytes(&loc.download_url)
            .with_context(|| format!("downloading chunk {}", loc.hash))?;
        let plaintext = crypto::decrypt(&vault_key, &nonce, &ciphertext, loc.hash.as_bytes())
            .with_context(|| format!("decrypting chunk {}", loc.hash))?;
        chunk_plaintexts.insert(loc.hash.clone(), plaintext);
    }

    fs::create_dir_all(to).with_context(|| format!("creating {}", to.display()))?;
    for file in &manifest.files {
        let dest = safe_join(to, &file.path)
            .with_context(|| format!("manifest file path {:?} is unsafe", file.path))?;
        if let Some(parent) = dest.parent() {
            fs::create_dir_all(parent).with_context(|| format!("creating {}", parent.display()))?;
        }
        let contents = chunker::unpack(&file.chunks, &chunk_plaintexts)
            .with_context(|| format!("reassembling {}", file.path))?;
        fs::write(&dest, &contents).with_context(|| format!("writing {}", dest.display()))?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&dest, fs::Permissions::from_mode(file.mode))
                .with_context(|| format!("setting permissions on {}", dest.display()))?;
        }
    }

    println!("restore complete");
    Ok(())
}

/// Joins `relative` (forward-slash-separated, as stored in the manifest)
/// onto `base`, rejecting anything that would escape `base` — an absolute
/// path, a `..` component, or an empty component. A manifest is encrypted
/// end-to-end and the server never validates its contents, so a corrupted
/// or maliciously crafted manifest must not be able to write outside the
/// chosen restore directory.
fn safe_join(base: &Path, relative: &str) -> Result<PathBuf> {
    let mut out = base.to_path_buf();
    for part in relative.split('/') {
        match part {
            "" | "." => continue,
            ".." => bail!("path escapes the restore directory"),
            _ => out.push(part),
        }
    }
    Ok(out)
}
