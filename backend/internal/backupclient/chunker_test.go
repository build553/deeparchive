package backupclient

import "testing"

func decryptAll(t *testing.T, key VaultKey, chunks []EncryptedChunk) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte, len(chunks))
	for _, c := range chunks {
		pt, err := Decrypt(key, c.Nonce, c.Ciphertext, []byte(c.Hash))
		if err != nil {
			t.Fatal(err)
		}
		out[c.Hash] = pt
	}
	return out
}

func TestPackUnpackRoundTripSmallFile(t *testing.T) {
	key := mustKey(t)
	data := []byte("a small file, well under the minimum chunk size")
	packed, err := Pack(data, key, DefaultChunkerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(packed.Chunks) != 1 {
		t.Fatalf("expected exactly one chunk for a small file, got %d", len(packed.Chunks))
	}
	restored, err := Unpack(packed.ChunkRefs, decryptAll(t, key, packed.Chunks))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(data) {
		t.Fatal("restored content does not match original")
	}
}

func TestPackUnpackRoundTripEmptyFile(t *testing.T) {
	key := mustKey(t)
	packed, err := Pack(nil, key, DefaultChunkerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(packed.ChunkRefs) != 0 || len(packed.Chunks) != 0 {
		t.Fatal("empty file must produce zero chunks")
	}
	restored, err := Unpack(packed.ChunkRefs, map[string][]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 0 {
		t.Fatal("restored empty file must be empty")
	}
}

func TestPackUnpackRoundTripMultiChunkFile(t *testing.T) {
	key := mustKey(t)
	data := make([]byte, 6*1024*1024)
	var x uint64
	for i := range data {
		x = uint64(i) * 2654435761
		data[i] = byte(x)
	}
	packed, err := Pack(data, key, DefaultChunkerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(packed.Chunks) <= 1 {
		t.Fatalf("expected multiple chunks for 6 MiB of varied data, got %d", len(packed.Chunks))
	}

	var totalRefLen uint64
	for _, r := range packed.ChunkRefs {
		totalRefLen += r.Length
	}
	if totalRefLen != uint64(len(data)) {
		t.Fatalf("chunk refs total length %d != data length %d", totalRefLen, len(data))
	}

	restored, err := Unpack(packed.ChunkRefs, decryptAll(t, key, packed.Chunks))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(data) {
		t.Fatal("restored content does not match original")
	}
}

func TestIdenticalContentAlwaysProducesTheSameChunkHash(t *testing.T) {
	key := mustKey(t)
	data := make([]byte, 900*1024)
	for i := range data {
		data[i] = 0x42
	}

	first, err := Pack(data, key, DefaultChunkerConfig())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Pack(data, key, DefaultChunkerConfig())
	if err != nil {
		t.Fatal(err)
	}

	hashes := func(p PackedFile) []string {
		out := make([]string, len(p.ChunkRefs))
		for i, r := range p.ChunkRefs {
			out[i] = r.Hash
		}
		return out
	}
	h1, h2 := hashes(first), hashes(second)
	if len(h1) != len(h2) {
		t.Fatalf("hash count differs: %d vs %d", len(h1), len(h2))
	}
	for i := range h1 {
		if h1[i] != h2[i] {
			t.Fatalf("same content must yield the same chunk hashes: %q != %q", h1[i], h2[i])
		}
	}
	if string(first.Chunks[0].Ciphertext) == string(second.Chunks[0].Ciphertext) {
		t.Fatal("ciphertext must still differ (fresh random nonce each time)")
	}
}

func TestARepeatedWholeChunkDedupsWithinOneFile(t *testing.T) {
	key := mustKey(t)
	cfg := DefaultChunkerConfig()
	block := make([]byte, cfg.MaxSize+1)
	for i := range block {
		block[i] = 0x7e
	}
	data := append(append([]byte{}, block...), block...)

	packed, err := Pack(data, key, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(packed.ChunkRefs) < 3 {
		t.Fatalf("expected multiple cuts, got %d", len(packed.ChunkRefs))
	}
	if len(packed.Chunks) >= len(packed.ChunkRefs) {
		t.Fatalf("the second copy's chunks must dedup against the first: %d unique chunks for %d references",
			len(packed.Chunks), len(packed.ChunkRefs))
	}

	restored, err := Unpack(packed.ChunkRefs, decryptAll(t, key, packed.Chunks))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(data) {
		t.Fatal("restored content does not match original")
	}
}

func TestUnpackFailsLoudlyOnAMissingChunk(t *testing.T) {
	key := mustKey(t)
	packed, err := Pack([]byte("some content"), key, DefaultChunkerConfig())
	if err != nil {
		t.Fatal(err)
	}
	_, err = Unpack(packed.ChunkRefs, map[string][]byte{})
	if _, ok := err.(*MissingChunkError); !ok {
		t.Fatalf("expected a MissingChunkError, got %v", err)
	}
}

func TestEachPackedChunkDecryptsOnlyWithItsOwnHashAsAAD(t *testing.T) {
	key := mustKey(t)
	packed, err := Pack([]byte("content for aad binding check"), key, DefaultChunkerConfig())
	if err != nil {
		t.Fatal(err)
	}
	chunk := packed.Chunks[0]
	if _, err := Decrypt(key, chunk.Nonce, chunk.Ciphertext, []byte("not-the-real-hash")); err == nil {
		t.Fatal("expected decryption to fail with the wrong aad")
	}
	if _, err := Decrypt(key, chunk.Nonce, chunk.Ciphertext, []byte(chunk.Hash)); err != nil {
		t.Fatal(err)
	}
}
