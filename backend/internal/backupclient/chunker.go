package backupclient

// Content-defined chunking: splits a file on content boundaries (not
// fixed offsets) so inserting a byte near the start of a large file
// doesn't invalidate every chunk after it — most of the file still dedups
// against the previous snapshot. Uses the same gear-hash rolling-hash
// technique FastCDC is built on, with a single-mask cut rule rather than
// FastCDC's two-mask "normalized chunking" refinement — simpler to
// implement correctly, still genuinely content-defined.
//
// This deliberately does NOT need to reproduce crates/backup-core's Rust
// FastCDC cut points byte-for-byte: a snapshot's chunk boundaries are a
// backup-time choice recorded in that snapshot's own manifest
// (ChunkRef.offset/length) and only ever replayed from there at restore
// time, never recomputed — see this package's doc comment in crypto.go.
// What must match across both implementations is everything downstream
// of a cut: the content hash, the encryption, and the manifest shape.

type ChunkerConfig struct {
	MinSize int
	AvgSize int
	MaxSize int
}

// DefaultChunkerConfig: 256 KiB / 1 MiB / 4 MiB, the same sizing
// crates/backup-core uses. Reasonable for personal files (photos,
// documents); not tuned for e.g. VM disk images. Known limitation:
// whole-file-in-memory chunking means this isn't yet appropriate for
// files much larger than available RAM.
func DefaultChunkerConfig() ChunkerConfig {
	return ChunkerConfig{MinSize: 256 * 1024, AvgSize: 1024 * 1024, MaxSize: 4 * 1024 * 1024}
}

// gearTable is 256 pseudorandom 64-bit constants, one per possible byte
// value, generated deterministically at init (splitmix64 from a fixed
// seed) rather than hard-coded — the exact values don't need to match
// any other implementation (see the chunking-direction note above), they
// only need to be fixed and well-distributed for this process's lifetime.
var gearTable [256]uint64

func init() {
	seed := uint64(0x9E3779B97F4A7C15)
	next := func() uint64 {
		seed += 0x9E3779B97F4A7C15
		z := seed
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		return z ^ (z >> 31)
	}
	for i := range gearTable {
		gearTable[i] = next()
	}
}

// cutMask has approximately log2(AvgSize-MinSize) bits set, so a byte
// position satisfies (hash & mask) == 0 with probability roughly
// 1/(AvgSize-MinSize) — biasing the average chunk length toward AvgSize
// without the full normalized-chunking refinement.
func cutMask(cfg ChunkerConfig) uint64 {
	span := cfg.AvgSize - cfg.MinSize
	if span < 1 {
		span = 1
	}
	bits := 0
	for (1 << bits) < span {
		bits++
	}
	if bits > 63 {
		bits = 63
	}
	return (uint64(1) << bits) - 1
}

// cutPoints returns every chunk boundary offset in data (always ending
// with len(data)), each within [MinSize, MaxSize] except possibly the
// final chunk, which may be shorter. Pure, allocation-light, and used by
// both pack (encrypts each chunk) and anything that just wants the split
// points.
func cutPoints(data []byte, cfg ChunkerConfig) []int {
	if len(data) == 0 {
		return nil
	}
	mask := cutMask(cfg)
	var points []int
	start := 0
	for start < len(data) {
		remaining := len(data) - start
		if remaining <= cfg.MinSize {
			points = append(points, len(data))
			break
		}
		limit := cfg.MaxSize
		if remaining < limit {
			limit = remaining
		}

		var hash uint64
		cut := limit // default: forced cut at max_size (or EOF)
		for i := cfg.MinSize; i < limit; i++ {
			hash = (hash << 1) + gearTable[data[start+i]]
			if hash&mask == 0 {
				cut = i + 1
				break
			}
		}
		points = append(points, start+cut)
		start += cut
	}
	return points
}

// EncryptedChunk is an encrypted chunk, ready to be uploaded keyed by Hash.
type EncryptedChunk struct {
	Hash       string
	Nonce      [NonceLen]byte
	Ciphertext []byte
}

type PackedFile struct {
	// One entry per chunk, in file order — this is what goes into the
	// file's manifest entry.
	ChunkRefs []ChunkRef
	// Every *distinct* chunk this file produced, encrypted. A file with
	// repeated content (or content shared with earlier files in the
	// same call) appears here once; ChunkRefs may reference it more
	// than once via the same hash.
	Chunks []EncryptedChunk
}

// Pack splits data, encrypts each distinct chunk under key, and returns
// both the ordered reference list and the deduped encrypted chunks. An
// empty file produces zero chunks and zero refs — a valid, restorable
// "file with no content."
func Pack(data []byte, key VaultKey, cfg ChunkerConfig) (PackedFile, error) {
	var out PackedFile
	seen := make(map[string]struct{})

	offset := 0
	for _, end := range cutPoints(data, cfg) {
		plaintext := data[offset:end]
		hash := ContentHash(plaintext)

		if _, ok := seen[hash]; !ok {
			seen[hash] = struct{}{}
			enc, err := Encrypt(key, plaintext, []byte(hash))
			if err != nil {
				return PackedFile{}, err
			}
			out.Chunks = append(out.Chunks, EncryptedChunk{Hash: hash, Nonce: enc.Nonce, Ciphertext: enc.Ciphertext})
		}

		out.ChunkRefs = append(out.ChunkRefs, ChunkRef{
			Hash: hash, Offset: uint64(offset), Length: uint64(end - offset),
		})
		offset = end
	}
	return out, nil
}

// Unpack reassembles a file's plaintext from its chunk references, given
// a map of already-decrypted chunk plaintext by hash — pure data
// assembly, no I/O. Returns an error naming the missing hash if a
// referenced chunk is absent, rather than silently producing a truncated
// or corrupt file.
func Unpack(chunkRefs []ChunkRef, chunkPlaintexts map[string][]byte) ([]byte, error) {
	var total int
	for _, r := range chunkRefs {
		total += int(r.Length)
	}
	out := make([]byte, 0, total)
	for _, r := range chunkRefs {
		plaintext, ok := chunkPlaintexts[r.Hash]
		if !ok {
			return nil, &MissingChunkError{Hash: r.Hash}
		}
		if uint64(len(plaintext)) != r.Length {
			return nil, &ChunkLengthMismatchError{Hash: r.Hash, Got: len(plaintext), Want: r.Length}
		}
		out = append(out, plaintext...)
	}
	return out, nil
}
