//! Content-defined chunking (FastCDC) plus the glue that turns one file's
//! bytes into encrypted, content-addressed chunks ([`pack`]) and back
//! ([`unpack`]). Splitting on content boundaries, rather than fixed offsets,
//! means inserting a byte near the start of a large file does not
//! invalidate every chunk after it — most of the file still dedups against
//! the previous snapshot.

use std::collections::HashMap;

use fastcdc::v2020::FastCDC;

use crate::crypto::{self, VaultKey, NONCE_LEN};
use crate::error::{Error, Result};
use crate::manifest::ChunkRef;

#[derive(Debug, Clone, Copy)]
pub struct ChunkerConfig {
    pub min_size: usize,
    pub avg_size: usize,
    pub max_size: usize,
}

impl Default for ChunkerConfig {
    /// 256 KiB / 1 MiB / 4 MiB. Reasonable for personal files (photos,
    /// documents); not tuned for e.g. VM disk images. Known limitation:
    /// whole-file-in-memory chunking (see module docs) means this is not
    /// yet appropriate for files much larger than available RAM.
    fn default() -> Self {
        Self {
            min_size: 256 * 1024,
            avg_size: 1024 * 1024,
            max_size: 4 * 1024 * 1024,
        }
    }
}

/// An encrypted chunk, ready to be uploaded keyed by `hash`.
pub struct EncryptedChunk {
    pub hash: String,
    pub nonce: [u8; NONCE_LEN],
    pub ciphertext: Vec<u8>,
}

pub struct PackedFile {
    /// One entry per chunk, in file order — this is what goes into the
    /// file's manifest entry.
    pub chunk_refs: Vec<ChunkRef>,
    /// Every *distinct* chunk this file produced, encrypted. A file with
    /// repeated content (or content shared with earlier files in the same
    /// call) appears here once; `chunk_refs` may reference it more than
    /// once via the same hash.
    pub chunks: Vec<EncryptedChunk>,
}

/// Splits `data` with FastCDC, encrypts each distinct chunk under `key`,
/// and returns both the ordered reference list and the deduped encrypted
/// chunks. An empty file produces zero chunks and zero chunk_refs — that is
/// a valid, restorable "file with no content."
pub fn pack(data: &[u8], key: &VaultKey, config: &ChunkerConfig) -> PackedFile {
    let mut chunk_refs = Vec::new();
    let mut chunks = Vec::new();
    let mut seen = HashMap::<String, ()>::new();

    for c in FastCDC::new(data, config.min_size, config.avg_size, config.max_size) {
        let plaintext = &data[c.offset..c.offset + c.length];
        let hash = crypto::content_hash(plaintext);

        if !seen.contains_key(&hash) {
            seen.insert(hash.clone(), ());
            let enc = crypto::encrypt(key, plaintext, hash.as_bytes());
            chunks.push(EncryptedChunk {
                hash: hash.clone(),
                nonce: enc.nonce,
                ciphertext: enc.ciphertext,
            });
        }

        chunk_refs.push(ChunkRef {
            hash,
            offset: c.offset as u64,
            length: c.length as u64,
        });
    }

    PackedFile { chunk_refs, chunks }
}

/// Reassembles a file's plaintext from its chunk references, given a map of
/// already-decrypted chunk plaintext by hash (the caller is responsible for
/// fetching ciphertext from storage and decrypting it — see `backup-cli`'s
/// restore flow — this function is pure data assembly with no I/O).
/// Returns an error naming the missing hash if a referenced chunk is absent,
/// rather than silently producing a truncated or corrupt file.
pub fn unpack(
    chunk_refs: &[ChunkRef],
    chunk_plaintexts: &HashMap<String, Vec<u8>>,
) -> Result<Vec<u8>> {
    let total: usize = chunk_refs.iter().map(|r| r.length as usize).sum();
    let mut out = Vec::with_capacity(total);
    for r in chunk_refs {
        let plaintext = chunk_plaintexts.get(&r.hash).ok_or_else(|| {
            Error::Scan(format!(
                "missing chunk {} needed to reassemble file",
                r.hash
            ))
        })?;
        if plaintext.len() != r.length as usize {
            return Err(Error::Scan(format!(
                "chunk {} has length {}, expected {}",
                r.hash,
                plaintext.len(),
                r.length
            )));
        }
        out.extend_from_slice(plaintext);
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn decrypt_all(key: &VaultKey, chunks: &[EncryptedChunk]) -> HashMap<String, Vec<u8>> {
        chunks
            .iter()
            .map(|c| {
                let plaintext =
                    crypto::decrypt(key, &c.nonce, &c.ciphertext, c.hash.as_bytes()).unwrap();
                (c.hash.clone(), plaintext)
            })
            .collect()
    }

    #[test]
    fn pack_unpack_round_trip_small_file() {
        let key = VaultKey::generate();
        let data = b"a small file, well under the minimum chunk size".to_vec();
        let packed = pack(&data, &key, &ChunkerConfig::default());
        assert_eq!(packed.chunks.len(), 1, "a small file is exactly one chunk");

        let plaintexts = decrypt_all(&key, &packed.chunks);
        let restored = unpack(&packed.chunk_refs, &plaintexts).unwrap();
        assert_eq!(restored, data);
    }

    #[test]
    fn pack_unpack_round_trip_empty_file() {
        let key = VaultKey::generate();
        let packed = pack(&[], &key, &ChunkerConfig::default());
        assert!(packed.chunk_refs.is_empty());
        assert!(packed.chunks.is_empty());
        let restored = unpack(&packed.chunk_refs, &HashMap::new()).unwrap();
        assert!(restored.is_empty());
    }

    #[test]
    fn pack_unpack_round_trip_multi_chunk_file() {
        let key = VaultKey::generate();
        // Several MiB of pseudo-random (non-repeating) data so it reliably
        // splits into more than one chunk at the default sizing.
        let mut data = vec![0u8; 6 * 1024 * 1024];
        for (i, b) in data.iter_mut().enumerate() {
            *b = (i as u64).wrapping_mul(2654435761).to_le_bytes()[0];
        }
        let packed = pack(&data, &key, &ChunkerConfig::default());
        assert!(
            packed.chunks.len() > 1,
            "expected multiple chunks for 6 MiB of varied data"
        );

        let total_ref_len: u64 = packed.chunk_refs.iter().map(|r| r.length).sum();
        assert_eq!(total_ref_len, data.len() as u64);

        let plaintexts = decrypt_all(&key, &packed.chunks);
        let restored = unpack(&packed.chunk_refs, &plaintexts).unwrap();
        assert_eq!(restored, data);
    }

    #[test]
    fn identical_content_always_produces_the_same_chunk_hash() {
        // The property dedup actually relies on: two files (or a file backed
        // up twice, unchanged) with the same bytes must get the same content
        // hash, so the caller (backup-cli) can recognize "already have this"
        // and skip the upload — even though each encryption used a fresh
        // random nonce, so the ciphertext itself differs every time.
        let key = VaultKey::generate();
        let data = vec![0x42u8; 900 * 1024];

        let first = pack(&data, &key, &ChunkerConfig::default());
        let second = pack(&data, &key, &ChunkerConfig::default());

        let hashes = |p: &PackedFile| -> Vec<String> {
            p.chunk_refs.iter().map(|r| r.hash.clone()).collect()
        };
        assert_eq!(
            hashes(&first),
            hashes(&second),
            "same content must yield the same chunk hashes"
        );
        assert_ne!(
            first.chunks[0].ciphertext, second.chunks[0].ciphertext,
            "ciphertext must still differ (fresh random nonce each time)"
        );
    }

    #[test]
    fn a_repeated_whole_chunk_dedups_within_one_file() {
        // Two back-to-back copies of a buffer large enough to exceed max_size
        // on its own: FastCDC is forced to cut at least once per copy, so the
        // second copy's chunk(s) must repeat the first copy's hash(es).
        let key = VaultKey::generate();
        let config = ChunkerConfig::default();
        let block = vec![0x7eu8; config.max_size + 1];
        let mut data = block.clone();
        data.extend_from_slice(&block);

        let packed = pack(&data, &key, &config);
        assert!(
            packed.chunk_refs.len() >= 3,
            "expected multiple cuts, got {:?}",
            packed.chunk_refs.len()
        );
        assert!(
            packed.chunks.len() < packed.chunk_refs.len(),
            "the second copy's chunks must dedup against the first: {} unique chunks for {} references",
            packed.chunks.len(),
            packed.chunk_refs.len()
        );

        let plaintexts = decrypt_all(&key, &packed.chunks);
        let restored = unpack(&packed.chunk_refs, &plaintexts).unwrap();
        assert_eq!(restored, data);
    }

    #[test]
    fn unpack_fails_loudly_on_a_missing_chunk() {
        let key = VaultKey::generate();
        let packed = pack(b"some content", &key, &ChunkerConfig::default());
        let err = unpack(&packed.chunk_refs, &HashMap::new()).unwrap_err();
        assert!(matches!(err, Error::Scan(_)));
    }

    #[test]
    fn each_packed_chunk_decrypts_only_with_its_own_hash_as_aad() {
        // pack() binds each chunk's content hash as AAD (see crypto::encrypt);
        // confirm that binding actually happened, not just that decrypt(with
        // the right hash) works, which tampered-AAD coverage already proves
        // in crypto::tests.
        let key = VaultKey::generate();
        let packed = pack(
            b"content for aad binding check",
            &key,
            &ChunkerConfig::default(),
        );
        let chunk = &packed.chunks[0];
        assert!(
            crypto::decrypt(&key, &chunk.nonce, &chunk.ciphertext, b"not-the-real-hash").is_err()
        );
        assert!(
            crypto::decrypt(&key, &chunk.nonce, &chunk.ciphertext, chunk.hash.as_bytes()).is_ok()
        );
    }
}
