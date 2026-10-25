use thiserror::Error;

#[derive(Debug, Error)]
pub enum Error {
    #[error("scanning filesystem: {0}")]
    Scan(String),

    #[error("reading file {path}: {source}")]
    ReadFile {
        path: String,
        #[source]
        source: std::io::Error,
    },

    #[error("invalid passphrase or corrupted vault key file")]
    WrongPassphraseOrCorruptVault,

    #[error("decryption failed: ciphertext is corrupt or was tampered with")]
    DecryptionFailed,

    #[error("unsupported vault key format version {0}")]
    UnsupportedVaultFormat(u32),

    #[error("unsupported manifest format version {0}")]
    UnsupportedManifestFormat(u32),

    #[error("serializing manifest: {0}")]
    Serialize(#[from] serde_json::Error),

    #[error("invalid hex/base64 encoding: {0}")]
    Encoding(String),
}

pub type Result<T> = std::result::Result<T, Error>;
