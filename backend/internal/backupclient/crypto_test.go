package backupclient

import "testing"

func mustKey(t *testing.T) VaultKey {
	t.Helper()
	k, err := GenerateVaultKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestChunkRoundTrip(t *testing.T) {
	key := mustKey(t)
	plaintext := []byte("some archive bytes, could be anything")
	aad := []byte(ContentHash(plaintext))
	enc, err := Encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decrypt(key, enc.Nonce, enc.Ciphertext, aad)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("round trip mismatch: got %q", got)
	}
}

func TestTamperedCiphertextIsRejected(t *testing.T) {
	key := mustKey(t)
	enc, err := Encrypt(key, []byte("secret"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	enc.Ciphertext[0] ^= 0xFF
	if _, err := Decrypt(key, enc.Nonce, enc.Ciphertext, []byte("aad")); err == nil {
		t.Fatal("expected decryption to fail on tampered ciphertext")
	}
}

func TestMismatchedAADIsRejected(t *testing.T) {
	key := mustKey(t)
	enc, err := Encrypt(key, []byte("secret"), []byte("chunk-a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(key, enc.Nonce, enc.Ciphertext, []byte("chunk-b")); err == nil {
		t.Fatal("expected decryption to fail on mismatched aad")
	}
}

func TestWrongKeyIsRejected(t *testing.T) {
	keyA, keyB := mustKey(t), mustKey(t)
	enc, err := Encrypt(keyA, []byte("secret"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(keyB, enc.Nonce, enc.Ciphertext, []byte("aad")); err == nil {
		t.Fatal("expected decryption to fail with the wrong key")
	}
}

func TestVaultKeyWrapUnwrapRoundTrip(t *testing.T) {
	key := mustKey(t)
	wrapped, err := WrapVaultKey(key, "a correct passphrase")
	if err != nil {
		t.Fatal(err)
	}
	unwrapped, err := wrapped.Unwrap("a correct passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if key != unwrapped {
		t.Fatal("unwrapped key does not match original")
	}
}

func TestVaultKeyWrongPassphraseIsRejected(t *testing.T) {
	key := mustKey(t)
	wrapped, err := WrapVaultKey(key, "correct passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrapped.Unwrap("wrong passphrase"); err == nil {
		t.Fatal("expected unwrap to fail with the wrong passphrase")
	}
}

func TestContentHashIsStableAndDistinguishesContent(t *testing.T) {
	if ContentHash([]byte("abc")) != ContentHash([]byte("abc")) {
		t.Fatal("same content must hash the same")
	}
	if ContentHash([]byte("abc")) == ContentHash([]byte("abd")) {
		t.Fatal("different content must hash differently")
	}
}
