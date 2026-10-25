//! Local persisted client state: the device's identity (so `backup` can
//! authenticate without re-enrolling) and the wrapped vault key (so a
//! passphrase, not a randomly generated key, is the only thing that must be
//! remembered to decrypt a backup). Written once by `init`, read by every
//! other command.

use std::fs;
use std::path::Path;

use anyhow::{Context, Result};
use backup_core::crypto::WrappedVaultKey;
use base64::engine::general_purpose::STANDARD as base64;
use base64::Engine;
use ed25519_dalek::SigningKey;
use serde::{Deserialize, Serialize};

#[derive(Serialize, Deserialize)]
pub struct DeviceState {
    pub device_id: String,
    /// Base64 of the 32-byte Ed25519 seed. This file is the device's entire
    /// credential — anyone who reads it can impersonate the device to the
    /// API. It is not itself encrypted (unlike the vault key below, which is
    /// additionally protected by a passphrase the device doesn't need to
    /// remember on its own to authenticate); protecting it is a filesystem
    /// permissions concern, same as an SSH private key.
    device_signing_key_seed: String,
    pub wrapped_vault_key: WrappedVaultKey,
}

/// A signed-in user's session, written by `login` (the device-code flow)
/// and read by every command that needs a user identity instead of a
/// device identity (`init`, `list`, `restore`) when `--email`/`--password`
/// aren't given. Deliberately separate from DeviceState: this is "who is
/// running the CLI," that is "which enrolled machine this is" — a single
/// login can drive `init` on several machines, and `backup`/heartbeat
/// never need a user session at all.
#[derive(Serialize, Deserialize)]
pub struct UserCredentials {
    pub access_token: String,
    pub refresh_token: String,
}

impl UserCredentials {
    pub fn load(path: &Path) -> Result<Self> {
        let bytes = fs::read(path)
            .with_context(|| format!("reading credentials file {}", path.display()))?;
        serde_json::from_slice(&bytes)
            .with_context(|| format!("credentials file {} is corrupt", path.display()))
    }

    pub fn save(&self, path: &Path) -> Result<()> {
        let bytes = serde_json::to_vec_pretty(self)?;
        fs::write(path, bytes)
            .with_context(|| format!("writing credentials file {}", path.display()))?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            // A bearer-equivalent refresh token — not world-readable,
            // same reasoning as the device signing key above.
            fs::set_permissions(path, fs::Permissions::from_mode(0o600))
                .with_context(|| format!("setting permissions on {}", path.display()))?;
        }
        Ok(())
    }
}

impl DeviceState {
    pub fn new(
        device_id: String,
        signing_key: &SigningKey,
        wrapped_vault_key: WrappedVaultKey,
    ) -> Self {
        Self {
            device_id,
            device_signing_key_seed: base64.encode(signing_key.to_bytes()),
            wrapped_vault_key,
        }
    }

    pub fn signing_key(&self) -> Result<SigningKey> {
        let seed_bytes = base64
            .decode(&self.device_signing_key_seed)
            .context("device state file is corrupt: signing key seed is not valid base64")?;
        let seed: [u8; 32] = seed_bytes.try_into().map_err(|_| {
            anyhow::anyhow!("device state file is corrupt: signing key seed is not 32 bytes")
        })?;
        Ok(SigningKey::from_bytes(&seed))
    }

    pub fn load(path: &Path) -> Result<Self> {
        let bytes = fs::read(path).with_context(|| {
            format!(
                "reading device state file {}: run `init` first",
                path.display()
            )
        })?;
        serde_json::from_slice(&bytes)
            .with_context(|| format!("device state file {} is corrupt", path.display()))
    }

    pub fn save(&self, path: &Path) -> Result<()> {
        let bytes = serde_json::to_vec_pretty(self)?;
        fs::write(path, bytes)
            .with_context(|| format!("writing device state file {}", path.display()))?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            // Contains the device's bearer-equivalent private key — not
            // world-readable.
            fs::set_permissions(path, fs::Permissions::from_mode(0o600))
                .with_context(|| format!("setting permissions on {}", path.display()))?;
        }
        Ok(())
    }
}
