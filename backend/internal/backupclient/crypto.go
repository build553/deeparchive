// Package backupclient is deepctl's client-side half of the backup
// protocol: scanning, content-defined chunking, encryption, and the
// manifest format. The server (API + object storage) never sees a
// plaintext byte, a plaintext file name, or the vault key — see
// docs/encryption.md for the full threat model.
//
// This mirrors crates/backup-core (Rust), which the desktop app embeds
// via Tauri — they are two independent implementations of the same wire
// format, not shared code, so every primitive here (AES-256-GCM, the
// AAD strings, the Argon2id parameters, the manifest JSON shape, the
// SHA-256 content hash) is deliberately kept identical to Rust's so a
// snapshot made by either client restores correctly on the other. The
// one piece that does NOT need to match is the chunker's exact cut
// points (see chunker.go) — chunk boundaries are a backup-time encoding
// choice recorded in that snapshot's own manifest, never recomputed at
// restore time, so two implementations splitting the same file
// differently is harmless.
package backupclient

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

const (
	KeyLen   = 32
	NonceLen = 12
	saltLen  = 16
)

var (
	ErrDecryptionFailed         = errors.New("decryption failed")
	ErrWrongPassphraseOrCorrupt = errors.New("wrong passphrase, or the vault file is corrupt")
)

// VaultKey is the random key that encrypts every chunk and manifest in
// one vault. Held only in memory in plaintext form; persisted exclusively
// as a WrappedVaultKey.
type VaultKey [KeyLen]byte

func GenerateVaultKey() (VaultKey, error) {
	var k VaultKey
	if _, err := rand.Read(k[:]); err != nil {
		return VaultKey{}, fmt.Errorf("reading OS randomness: %w", err)
	}
	return k, nil
}

func (k VaultKey) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Argon2id parameters for turning a passphrase into a key-wrapping key —
// OWASP's current minimum (19 MiB, t=2, p=1), the same baseline
// backend/internal/identity/password.go uses for login passwords and
// crates/backup-core/src/crypto.rs uses for this same purpose, so the
// project has one consistent KDF story instead of several.
const (
	argonMemoryKB = 19 * 1024
	argonTime     = 2
	argonThreads  = 1
)

func deriveKEK(passphrase string, salt []byte) VaultKey {
	var kek VaultKey
	copy(kek[:], argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemoryKB, argonThreads, KeyLen))
	return kek
}

// WrappedVaultKey is a VaultKey encrypted under a passphrase-derived key
// — the only form the vault key is ever written to disk in. Field names
// and hex encoding match crates/backup-core's serde JSON shape exactly.
type WrappedVaultKey struct {
	FormatVersion uint32 `json:"format_version"`
	Salt          string `json:"salt"`
	Nonce         string `json:"nonce"`
	Ciphertext    string `json:"ciphertext"`
}

const vaultFormatVersion = 1

func WrapVaultKey(key VaultKey, passphrase string) (WrappedVaultKey, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return WrappedVaultKey{}, fmt.Errorf("reading OS randomness: %w", err)
	}
	kek := deriveKEK(passphrase, salt)

	nonce := make([]byte, NonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return WrappedVaultKey{}, fmt.Errorf("reading OS randomness: %w", err)
	}
	gcm, err := kek.gcm()
	if err != nil {
		return WrappedVaultKey{}, err
	}
	ciphertext := gcm.Seal(nil, nonce, key[:], nil)

	return WrappedVaultKey{
		FormatVersion: vaultFormatVersion,
		Salt:          hex.EncodeToString(salt),
		Nonce:         hex.EncodeToString(nonce),
		Ciphertext:    hex.EncodeToString(ciphertext),
	}, nil
}

func (w WrappedVaultKey) Unwrap(passphrase string) (VaultKey, error) {
	if w.FormatVersion != vaultFormatVersion {
		return VaultKey{}, fmt.Errorf("unsupported vault format version %d", w.FormatVersion)
	}
	salt, err := hex.DecodeString(w.Salt)
	if err != nil {
		return VaultKey{}, ErrWrongPassphraseOrCorrupt
	}
	nonce, err := hex.DecodeString(w.Nonce)
	if err != nil || len(nonce) != NonceLen {
		return VaultKey{}, ErrWrongPassphraseOrCorrupt
	}
	ciphertext, err := hex.DecodeString(w.Ciphertext)
	if err != nil {
		return VaultKey{}, ErrWrongPassphraseOrCorrupt
	}

	kek := deriveKEK(passphrase, salt)
	gcm, err := kek.gcm()
	if err != nil {
		return VaultKey{}, err
	}
	// A wrong passphrase derives a different KEK, so GCM authentication
	// fails — one conservative error for both cases, not a confusable
	// "wrong passphrase" vs "corrupt file" distinction.
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil || len(plaintext) != KeyLen {
		return VaultKey{}, ErrWrongPassphraseOrCorrupt
	}
	var key VaultKey
	copy(key[:], plaintext)
	return key, nil
}

// Encrypted is an encrypted chunk or manifest blob, ready to
// upload/store. Nonce MUST be persisted alongside Ciphertext — losing it
// makes the ciphertext permanently undecryptable even with the right key.
type Encrypted struct {
	Nonce      [NonceLen]byte
	Ciphertext []byte
}

// Encrypt encrypts plaintext with a fresh random nonce. aad (associated
// data, e.g. the chunk's content hash) is authenticated but not
// encrypted — binding it in prevents an attacker who controls storage
// from swapping one legitimately-encrypted chunk's ciphertext onto a
// different chunk's metadata without detection.
func Encrypt(key VaultKey, plaintext, aad []byte) (Encrypted, error) {
	gcm, err := key.gcm()
	if err != nil {
		return Encrypted{}, err
	}
	var nonce [NonceLen]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Encrypted{}, fmt.Errorf("reading OS randomness: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce[:], plaintext, aad)
	return Encrypted{Nonce: nonce, Ciphertext: ciphertext}, nil
}

func Decrypt(key VaultKey, nonce [NonceLen]byte, ciphertext, aad []byte) ([]byte, error) {
	gcm, err := key.gcm()
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nonce[:], ciphertext, aad)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return plaintext, nil
}

// ContentHash is the hash used for chunk identity and dedup — a hash of
// the *plaintext*. See docs/encryption.md for what that leaks (equality
// of chunk contents is detectable by the server) and why that tradeoff
// is accepted.
func ContentHash(plaintext []byte) string {
	sum := sha256.Sum256(plaintext)
	return hex.EncodeToString(sum[:])
}
