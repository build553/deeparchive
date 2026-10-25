package main

// Local persisted client state: the device's identity (so `backup` can
// authenticate without re-enrolling) and the wrapped vault key (so a
// passphrase, not a randomly generated key, is the only thing that must
// be remembered to decrypt a backup) — written once by `init`, read by
// every other device command. UserCredentials is a separate file: who is
// running the CLI, written by `login`, read by any command that needs a
// user session instead of a device session.

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"deeparchive/internal/backupclient"
)

type DeviceState struct {
	DeviceID string `json:"device_id"`
	// Base64 of the 32-byte Ed25519 seed. This file is the device's
	// entire credential — anyone who reads it can impersonate the
	// device to the API. Not itself encrypted (unlike the vault key
	// below, additionally protected by a passphrase); protecting it is
	// a filesystem-permissions concern, same as an SSH private key.
	DeviceSigningKeySeed string                       `json:"device_signing_key_seed"`
	WrappedVaultKey      backupclient.WrappedVaultKey `json:"wrapped_vault_key"`
}

func NewDeviceState(deviceID string, seed []byte, wrapped backupclient.WrappedVaultKey) DeviceState {
	return DeviceState{
		DeviceID:             deviceID,
		DeviceSigningKeySeed: base64.StdEncoding.EncodeToString(seed),
		WrappedVaultKey:      wrapped,
	}
}

func (s DeviceState) SigningKey() (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(s.DeviceSigningKeySeed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("device state file is corrupt: signing key seed is not a valid %d-byte seed", ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func LoadDeviceState(path string) (DeviceState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return DeviceState{}, fmt.Errorf("reading device state file %s: run `init` first: %w", path, err)
	}
	var s DeviceState
	if err := json.Unmarshal(b, &s); err != nil {
		return DeviceState{}, fmt.Errorf("device state file %s is corrupt: %w", path, err)
	}
	return s, nil
}

func (s DeviceState) Save(path string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	// 0600: contains the device's bearer-equivalent private key — not
	// world-readable.
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("writing device state file %s: %w", path, err)
	}
	return nil
}

// UserCredentials is a signed-in user's session, written by `login` (the
// device-code flow) and read by every command that needs a user
// identity instead of a device identity (`init`, `list`, `restore`).
// Deliberately separate from DeviceState: this is "who is running the
// CLI," that is "which enrolled machine this is" — one login can drive
// `init` on several machines, and `backup`/heartbeat never need a user
// session at all.
type UserCredentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	// Server is the API this sign-in was made against, remembered so
	// later commands need no --server flag.
	Server string `json:"server,omitempty"`
}

func LoadUserCredentials(path string) (UserCredentials, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return UserCredentials{}, err
	}
	var c UserCredentials
	if err := json.Unmarshal(b, &c); err != nil {
		return UserCredentials{}, fmt.Errorf("credentials file %s is corrupt: %w", path, err)
	}
	return c, nil
}

func (c UserCredentials) Save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	// 0600: a bearer-equivalent refresh token — not world-readable, same
	// reasoning as the device signing key above.
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("writing credentials file %s: %w", path, err)
	}
	return nil
}
