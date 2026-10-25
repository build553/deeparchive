//! Client-side encryption. The server (Go API + object storage) never sees a
//! plaintext byte, a plaintext file name, or the vault key: it stores
//! encrypted chunks and an encrypted manifest blob, addressed only by
//! content hash and opaque IDs. See `docs/encryption.md` for the full
//! threat model and the tradeoffs this makes explicit rather than hiding.
//!
//! Known limitation (documented, not silently accepted): chunk nonces are
//! random, not a counter, so there is a birthday-bound collision risk once a
//! single vault key has encrypted on the order of 2^32 chunks (NIST's
//! guidance for 96-bit GCM nonces with random generation). A personal backup
//! vault is extremely unlikely to reach that volume, but vault key rotation
//! (re-encrypting with a fresh key) should be implemented before this is
//! used at a scale where it could.

use aes_gcm::aead::{Aead, KeyInit, Payload};
use aes_gcm::Aes256Gcm;
use argon2::{Algorithm, Argon2, Params, Version};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::error::{Error, Result};

pub const KEY_LEN: usize = 32;
pub const NONCE_LEN: usize = 12;
const SALT_LEN: usize = 16;

/// Fills `buf` with OS-sourced random bytes (suitable for keys/nonces/salts).
/// `getrandom` is used directly, rather than through `rand`'s higher-level
/// RNG traits, to keep this crate's one source of randomness small and
/// easy to audit.
fn fill_random(buf: &mut [u8]) {
    getrandom::fill(buf).expect("reading OS randomness cannot fail on a supported platform");
}

/// The random key that encrypts every chunk and manifest in one vault
/// (roughly: one backup identity). Held only in memory in plaintext form;
/// persisted exclusively as a [`WrappedVaultKey`].
#[derive(Clone)]
pub struct VaultKey([u8; KEY_LEN]);

impl VaultKey {
    pub fn generate() -> Self {
        let mut bytes = [0u8; KEY_LEN];
        fill_random(&mut bytes);
        Self(bytes)
    }

    fn cipher(&self) -> Aes256Gcm {
        Aes256Gcm::new(&self.0.into())
    }
}

/// Argon2id parameters used to turn a user passphrase into a key-wrapping
/// key. Matches OWASP's current minimum (19 MiB, t=2, p=1) — the same
/// baseline the Go side uses for password hashing (see
/// `backend/internal/identity/password.go`), so the project tells one
/// consistent story about its KDF choice rather than two different ones.
fn argon2_params() -> Params {
    Params::new(19_456, 2, 1, Some(KEY_LEN)).expect("static Argon2id parameters are valid")
}

fn derive_kek(passphrase: &str, salt: &[u8]) -> Result<[u8; KEY_LEN]> {
    let argon2 = Argon2::new(Algorithm::Argon2id, Version::V0x13, argon2_params());
    let mut kek = [0u8; KEY_LEN];
    argon2
        .hash_password_into(passphrase.as_bytes(), salt, &mut kek)
        .map_err(|e| Error::Encoding(format!("deriving key-wrapping key: {e}")))?;
    Ok(kek)
}

/// A [`VaultKey`] encrypted ("wrapped") under a key derived from a user
/// passphrase via Argon2id. This is the only form in which the vault key is
/// ever written to disk. Serializes to JSON for `backup-cli` to persist
/// (typically as a single local file the user is told to back up
/// separately — losing both the passphrase and this file means the backup
/// is permanently unrecoverable; there is no server-side key escrow by
/// design).
#[derive(Serialize, Deserialize)]
pub struct WrappedVaultKey {
    format_version: u32,
    salt: String,       // hex
    nonce: String,      // hex
    ciphertext: String, // hex; the wrapped key + GCM tag
}

const VAULT_FORMAT_VERSION: u32 = 1;

impl WrappedVaultKey {
    pub fn wrap(key: &VaultKey, passphrase: &str) -> Result<Self> {
        let mut salt = [0u8; SALT_LEN];
        fill_random(&mut salt);
        let kek = derive_kek(passphrase, &salt)?;

        let mut nonce_bytes = [0u8; NONCE_LEN];
        fill_random(&mut nonce_bytes);
        let cipher = Aes256Gcm::new(&kek.into());
        let ciphertext = cipher
            .encrypt(&nonce_bytes.into(), key.0.as_slice())
            .map_err(|_| Error::DecryptionFailed)?;

        Ok(Self {
            format_version: VAULT_FORMAT_VERSION,
            salt: hex::encode(salt),
            nonce: hex::encode(nonce_bytes),
            ciphertext: hex::encode(ciphertext),
        })
    }

    pub fn unwrap(&self, passphrase: &str) -> Result<VaultKey> {
        if self.format_version != VAULT_FORMAT_VERSION {
            return Err(Error::UnsupportedVaultFormat(self.format_version));
        }
        let salt = decode_hex(&self.salt)?;
        let nonce: [u8; NONCE_LEN] = decode_hex(&self.nonce)?
            .try_into()
            .map_err(|_| Error::WrongPassphraseOrCorruptVault)?;
        let ciphertext = decode_hex(&self.ciphertext)?;

        let kek = derive_kek(passphrase, &salt)?;
        let cipher = Aes256Gcm::new(&kek.into());
        let plaintext = cipher
            .decrypt(&nonce.into(), ciphertext.as_slice())
            // A wrong passphrase derives a different KEK, so GCM
            // authentication fails — not a confusable error class, so one
            // conservative message that doesn't hint which it was.
            .map_err(|_| Error::WrongPassphraseOrCorruptVault)?;

        let key: [u8; KEY_LEN] = plaintext
            .try_into()
            .map_err(|_| Error::WrongPassphraseOrCorruptVault)?;
        Ok(VaultKey(key))
    }
}

fn decode_hex(s: &str) -> Result<Vec<u8>> {
    hex::decode(s).map_err(|e| Error::Encoding(e.to_string()))
}

/// An encrypted chunk or manifest blob, ready to upload/store. `nonce` MUST
/// be persisted alongside `ciphertext` — losing it makes the ciphertext
/// permanently undecryptable even with the correct key.
pub struct Encrypted {
    pub nonce: [u8; NONCE_LEN],
    pub ciphertext: Vec<u8>,
}

/// Encrypts `plaintext` with a fresh random nonce. `aad` (associated data,
/// e.g. the chunk's content hash) is authenticated but not encrypted —
/// binding it in prevents an attacker who controls storage from swapping one
/// legitimately-encrypted chunk's ciphertext onto a different chunk's
/// metadata without detection.
pub fn encrypt(key: &VaultKey, plaintext: &[u8], aad: &[u8]) -> Encrypted {
    let mut nonce_bytes = [0u8; NONCE_LEN];
    fill_random(&mut nonce_bytes);
    let ciphertext = key
        .cipher()
        .encrypt(
            &nonce_bytes.into(),
            Payload {
                msg: plaintext,
                aad,
            },
        )
        .expect("AES-256-GCM encryption of an in-memory buffer cannot fail");
    Encrypted {
        nonce: nonce_bytes,
        ciphertext,
    }
}

pub fn decrypt(
    key: &VaultKey,
    nonce: &[u8; NONCE_LEN],
    ciphertext: &[u8],
    aad: &[u8],
) -> Result<Vec<u8>> {
    key.cipher()
        .decrypt(
            nonce.into(),
            Payload {
                msg: ciphertext,
                aad,
            },
        )
        .map_err(|_| Error::DecryptionFailed)
}

/// Content hash used for chunk identity and dedup. This is a hash of the
/// *plaintext* — see the module-level and `docs/encryption.md` discussion of
/// what that leaks (equality of chunk contents is detectable by the server)
/// and why that tradeoff is accepted.
pub fn content_hash(plaintext: &[u8]) -> String {
    hex::encode(Sha256::digest(plaintext))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn chunk_round_trip() {
        let key = VaultKey::generate();
        let plaintext = b"some archive bytes, could be anything";
        let aad = content_hash(plaintext);
        let enc = encrypt(&key, plaintext, aad.as_bytes());
        let decrypted = decrypt(&key, &enc.nonce, &enc.ciphertext, aad.as_bytes()).unwrap();
        assert_eq!(decrypted, plaintext);
    }

    #[test]
    fn tampered_ciphertext_is_rejected() {
        let key = VaultKey::generate();
        let aad = b"aad";
        let mut enc = encrypt(&key, b"secret", aad);
        enc.ciphertext[0] ^= 0xFF;
        assert!(decrypt(&key, &enc.nonce, &enc.ciphertext, aad).is_err());
    }

    #[test]
    fn mismatched_aad_is_rejected() {
        let key = VaultKey::generate();
        let enc = encrypt(&key, b"secret", b"chunk-a");
        // Same key, same ciphertext/nonce, but claiming it's a different
        // chunk's AAD — must fail, or chunk metadata could be swapped.
        assert!(decrypt(&key, &enc.nonce, &enc.ciphertext, b"chunk-b").is_err());
    }

    #[test]
    fn wrong_key_is_rejected() {
        let key_a = VaultKey::generate();
        let key_b = VaultKey::generate();
        let enc = encrypt(&key_a, b"secret", b"aad");
        assert!(decrypt(&key_b, &enc.nonce, &enc.ciphertext, b"aad").is_err());
    }

    #[test]
    fn vault_key_wrap_unwrap_round_trip() {
        let key = VaultKey::generate();
        let wrapped = WrappedVaultKey::wrap(&key, "a correct passphrase").unwrap();
        let unwrapped = wrapped.unwrap("a correct passphrase").unwrap();
        assert_eq!(key.0, unwrapped.0);
    }

    #[test]
    fn vault_key_wrong_passphrase_is_rejected() {
        let key = VaultKey::generate();
        let wrapped = WrappedVaultKey::wrap(&key, "correct passphrase").unwrap();
        assert!(wrapped.unwrap("wrong passphrase").is_err());
    }

    #[test]
    fn wrapped_vault_key_serializes_to_json() {
        let key = VaultKey::generate();
        let wrapped = WrappedVaultKey::wrap(&key, "passphrase").unwrap();
        let json = serde_json::to_string(&wrapped).unwrap();
        let back: WrappedVaultKey = serde_json::from_str(&json).unwrap();
        assert_eq!(back.unwrap("passphrase").unwrap().0, key.0);
    }

    #[test]
    fn content_hash_is_stable_and_distinguishes_content() {
        assert_eq!(content_hash(b"abc"), content_hash(b"abc"));
        assert_ne!(content_hash(b"abc"), content_hash(b"abd"));
    }
}
