package backupclient

// The backup manifest: the index of what a snapshot contains. Stored
// encrypted end-to-end — the server only ever sees EncryptedManifest
// bytes, never file paths, individual file sizes, or the chunk map in
// the clear. See docs/backup-protocol.md for how this fits the
// upload/commit/restore flow with the API, and this package's doc
// comment in crypto.go for why the JSON shape below is kept identical
// to crates/backup-core's serde output (so either client can restore a
// snapshot the other one made).

import (
	"encoding/json"
	"time"
)

const ManifestFormatVersion = 1

// manifestAAD binds ciphertext to "this is a manifest" so a manifest
// blob cannot be swapped in where a chunk is expected (or vice versa)
// even though both are "just AES-GCM ciphertext" to anyone without the
// key. Must match crates/backup-core's MANIFEST_AAD exactly.
var manifestAAD = []byte("deeparchive-manifest-v1")

type ChunkRef struct {
	// Hex SHA-256 of this chunk's plaintext — also the key into
	// Manifest.Chunks and the object storage key for the encrypted
	// chunk (see docs/backup-protocol.md).
	Hash string `json:"hash"`
	// Byte offset within the reconstructed file.
	Offset uint64 `json:"offset"`
	Length uint64 `json:"length"`
}

type ChunkMeta struct {
	Nonce          string `json:"nonce"` // hex, NonceLen bytes
	CiphertextSize uint64 `json:"ciphertext_size"`
}

type FileEntry struct {
	// Forward-slash-separated, relative to the backup root. Never an
	// absolute path — a restore must not be able to escape the restore
	// target directory (enforced in restore's path-joining, not just by
	// convention here).
	Path string `json:"path"`
	Size uint64 `json:"size"`
	// Unix permission bits. On non-Unix platforms this is a fixed
	// placeholder — restoring real ACLs/permissions on Windows isn't
	// implemented; documented limitation, not silently wrong.
	Mode     uint32     `json:"mode"`
	Modified string     `json:"modified"` // RFC 3339
	Chunks   []ChunkRef `json:"chunks"`
}

type Manifest struct {
	FormatVersion uint32      `json:"format_version"`
	CreatedAt     string      `json:"created_at"` // RFC 3339
	Files         []FileEntry `json:"files"`
	// Every distinct chunk referenced by Files, keyed by content hash. A
	// chunk appearing in two files (or twice in one file) is stored once.
	Chunks    map[string]ChunkMeta `json:"chunks"`
	TotalSize uint64               `json:"total_size"`
}

func NewManifest(files []FileEntry, chunks map[string]ChunkMeta) Manifest {
	var total uint64
	for _, f := range files {
		total += f.Size
	}
	return Manifest{
		FormatVersion: ManifestFormatVersion,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		Files:         files,
		Chunks:        chunks,
		TotalSize:     total,
	}
}

// ReferencedChunkHashes returns every chunk hash this manifest's files
// reference, de-duplicated, in no particular order — what the CLI asks
// the API "which of these do you already have" about before uploading.
func (m Manifest) ReferencedChunkHashes() []string {
	out := make([]string, 0, len(m.Chunks))
	for h := range m.Chunks {
		out = append(out, h)
	}
	return out
}

type EncryptedManifest struct {
	Nonce      [NonceLen]byte
	Ciphertext []byte
}

func (m Manifest) Encrypt(key VaultKey) (EncryptedManifest, error) {
	plaintext, err := json.Marshal(m)
	if err != nil {
		return EncryptedManifest{}, err
	}
	enc, err := Encrypt(key, plaintext, manifestAAD)
	if err != nil {
		return EncryptedManifest{}, err
	}
	return EncryptedManifest{Nonce: enc.Nonce, Ciphertext: enc.Ciphertext}, nil
}

func (e EncryptedManifest) Decrypt(key VaultKey) (Manifest, error) {
	plaintext, err := Decrypt(key, e.Nonce, e.Ciphertext, manifestAAD)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(plaintext, &m); err != nil {
		return Manifest{}, err
	}
	if m.FormatVersion != ManifestFormatVersion {
		return Manifest{}, &UnsupportedManifestFormatError{Version: m.FormatVersion}
	}
	return m, nil
}
