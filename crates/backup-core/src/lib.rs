//! `backup-core`: filesystem scanning, content-defined chunking,
//! client-side encryption, and the versioned backup manifest format.
//!
//! Pure library — no network, no filesystem writes beyond reading backup
//! source files, no CLI. `backup-cli` (sibling crate) drives this against
//! the real API and object storage.

pub mod chunker;
pub mod crypto;
pub mod error;
pub mod manifest;
pub mod scanner;

pub use chunker::{pack, unpack, ChunkerConfig, EncryptedChunk, PackedFile};
pub use crypto::VaultKey;
pub use error::{Error, Result};
pub use manifest::Manifest;
