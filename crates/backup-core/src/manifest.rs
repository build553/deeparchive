//! The backup manifest: the index of what a snapshot contains. Stored
//! encrypted end-to-end — the server only ever sees [`EncryptedManifest`]
//! bytes, never file paths, sizes of individual files, or the chunk map in
//! the clear. See `docs/backup-protocol.md` for how this fits into the
//! upload/commit/restore flow with the API.

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};
use time::format_description::well_known::Rfc3339;
use time::OffsetDateTime;

use crate::crypto::{self, VaultKey};
use crate::error::{Error, Result};

pub const MANIFEST_FORMAT_VERSION: u32 = 1;

/// Associated data binding ciphertext to "this is a manifest" so a manifest
/// blob cannot be swapped in where a chunk is expected (or vice versa) even
/// though both are "just AES-GCM ciphertext" to anyone without the key.
const MANIFEST_AAD: &[u8] = b"deeparchive-manifest-v1";

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ChunkRef {
    /// Hex SHA-256 of this chunk's plaintext — also the key into
    /// [`Manifest::chunks`] and the object storage key for the encrypted
    /// chunk (see `docs/backup-protocol.md`).
    pub hash: String,
    /// Byte offset within the reconstructed file.
    pub offset: u64,
    pub length: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ChunkMeta {
    pub nonce: String, // hex, crypto::NONCE_LEN bytes
    pub ciphertext_size: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct FileEntry {
    /// Forward-slash-separated, relative to the backup root. Never an
    /// absolute path — a restore must not be able to escape the restore
    /// target directory (enforced when reconstructing, not just by
    /// convention here; see backup-cli's restore path-joining).
    pub path: String,
    pub size: u64,
    /// Unix permission bits. On non-Unix platforms this is a fixed
    /// placeholder (0o644) — restoring real ACLs/permissions on Windows is
    /// not implemented yet; documented limitation, not silently wrong.
    pub mode: u32,
    pub modified: String, // RFC 3339
    pub chunks: Vec<ChunkRef>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Manifest {
    pub format_version: u32,
    pub created_at: String, // RFC 3339
    pub files: Vec<FileEntry>,
    /// Every distinct chunk referenced by `files`, keyed by content hash.
    /// A chunk appearing in two files (or twice in one file) is stored once.
    pub chunks: BTreeMap<String, ChunkMeta>,
    pub total_size: u64,
}

impl Manifest {
    pub fn new(files: Vec<FileEntry>, chunks: BTreeMap<String, ChunkMeta>) -> Self {
        let total_size = files.iter().map(|f| f.size).sum();
        Self {
            format_version: MANIFEST_FORMAT_VERSION,
            created_at: OffsetDateTime::now_utc()
                .format(&Rfc3339)
                .expect("formatting the current time as RFC3339 cannot fail"),
            files,
            chunks,
            total_size,
        }
    }

    /// Every chunk hash this manifest's files actually reference, in no
    /// particular order, de-duplicated. What `backup-cli` asks the API
    /// "which of these do you already have" about before uploading.
    pub fn referenced_chunk_hashes(&self) -> Vec<&str> {
        self.chunks.keys().map(String::as_str).collect()
    }

    pub fn encrypt(&self, key: &VaultKey) -> Result<EncryptedManifest> {
        let plaintext = serde_json::to_vec(self)?;
        let enc = crypto::encrypt(key, &plaintext, MANIFEST_AAD);
        Ok(EncryptedManifest {
            nonce: enc.nonce,
            ciphertext: enc.ciphertext,
        })
    }
}

/// An encrypted manifest, as sent to / received from the API.
pub struct EncryptedManifest {
    pub nonce: [u8; crypto::NONCE_LEN],
    pub ciphertext: Vec<u8>,
}

impl EncryptedManifest {
    pub fn decrypt(&self, key: &VaultKey) -> Result<Manifest> {
        let plaintext = crypto::decrypt(key, &self.nonce, &self.ciphertext, MANIFEST_AAD)?;
        let manifest: Manifest = serde_json::from_slice(&plaintext)?;
        if manifest.format_version != MANIFEST_FORMAT_VERSION {
            return Err(Error::UnsupportedManifestFormat(manifest.format_version));
        }
        Ok(manifest)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sample_manifest() -> Manifest {
        let mut chunks = BTreeMap::new();
        chunks.insert(
            "a".repeat(64),
            ChunkMeta {
                nonce: "00".repeat(crypto::NONCE_LEN),
                ciphertext_size: 123,
            },
        );
        let files = vec![FileEntry {
            path: "docs/report.pdf".into(),
            size: 100,
            mode: 0o644,
            modified: "2026-01-01T00:00:00Z".into(),
            chunks: vec![ChunkRef {
                hash: "a".repeat(64),
                offset: 0,
                length: 100,
            }],
        }];
        Manifest::new(files, chunks)
    }

    #[test]
    fn encrypt_decrypt_round_trip() {
        let key = VaultKey::generate();
        let manifest = sample_manifest();
        let encrypted = manifest.encrypt(&key).unwrap();
        let decrypted = encrypted.decrypt(&key).unwrap();
        assert_eq!(manifest, decrypted);
    }

    #[test]
    fn wrong_key_cannot_decrypt() {
        let manifest = sample_manifest();
        let encrypted = manifest.encrypt(&VaultKey::generate()).unwrap();
        assert!(encrypted.decrypt(&VaultKey::generate()).is_err());
    }

    #[test]
    fn unsupported_format_version_is_rejected() {
        let key = VaultKey::generate();
        let mut manifest = sample_manifest();
        manifest.format_version = 999;
        let encrypted = manifest.encrypt(&key).unwrap();
        let err = encrypted.decrypt(&key).unwrap_err();
        assert!(matches!(err, Error::UnsupportedManifestFormat(999)));
    }

    #[test]
    fn referenced_chunk_hashes_matches_chunk_map() {
        let manifest = sample_manifest();
        assert_eq!(
            manifest.referenced_chunk_hashes(),
            vec!["a".repeat(64).as_str()]
        );
    }
}
