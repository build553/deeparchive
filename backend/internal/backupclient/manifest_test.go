package backupclient

import "testing"

func sampleManifest() Manifest {
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	chunks := map[string]ChunkMeta{
		hash: {Nonce: "00000000000000000000000000000000000000000000", CiphertextSize: 123},
	}
	files := []FileEntry{{
		Path: "docs/report.pdf", Size: 100, Mode: 0o644, Modified: "2026-01-01T00:00:00Z",
		Chunks: []ChunkRef{{Hash: hash, Offset: 0, Length: 100}},
	}}
	return NewManifest(files, chunks)
}

func TestManifestEncryptDecryptRoundTrip(t *testing.T) {
	key := mustKey(t)
	m := sampleManifest()
	enc, err := m.Encrypt(key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := enc.Decrypt(key)
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatedAt != m.CreatedAt || len(got.Files) != len(m.Files) || len(got.Chunks) != len(m.Chunks) {
		t.Fatalf("round trip mismatch: %+v vs %+v", got, m)
	}
}

func TestManifestWrongKeyCannotDecrypt(t *testing.T) {
	m := sampleManifest()
	enc, err := m.Encrypt(mustKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Decrypt(mustKey(t)); err == nil {
		t.Fatal("expected decryption to fail with the wrong key")
	}
}

func TestManifestUnsupportedFormatVersionIsRejected(t *testing.T) {
	key := mustKey(t)
	m := sampleManifest()
	m.FormatVersion = 999
	enc, err := m.Encrypt(key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = enc.Decrypt(key)
	if _, ok := err.(*UnsupportedManifestFormatError); !ok {
		t.Fatalf("expected UnsupportedManifestFormatError, got %v", err)
	}
}

func TestReferencedChunkHashesMatchesChunkMap(t *testing.T) {
	m := sampleManifest()
	got := m.ReferencedChunkHashes()
	if len(got) != 1 || got[0] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected referenced hashes: %v", got)
	}
}
