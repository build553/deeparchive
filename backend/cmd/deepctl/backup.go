package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"deeparchive/internal/backupclient"
)

func newBackupCmd() *cobra.Command {
	var passphrase string
	var exclude []string
	cmd := &cobra.Command{
		Use:   "backup <directory>",
		Short: "Scan, chunk, encrypt, and back up a directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pass, err := passphraseOrPrompt(passphrase, false)
			if err != nil {
				return err
			}
			return runBackup(args[0], pass, exclude)
		},
	}
	cmd.Flags().StringVar(&passphrase, "passphrase", "", "Vault passphrase (default: asked securely on the terminal)")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "Gitignore-style exclude pattern (repeatable), e.g. --exclude '*.tmp'")
	return cmd
}

// authenticateDevice mirrors exactly what the API's devices.Service.Authenticate
// verifies: issue a challenge, sign its nonce with the device's private
// key, exchange the signature for a short-lived device access token.
func authenticateDevice(state DeviceState) (string, error) {
	signingKey, err := state.SigningKey()
	if err != nil {
		return "", err
	}
	challengeID, nonceB64, err := api().IssueChallenge(state.DeviceID)
	if err != nil {
		return "", fmt.Errorf("issuing challenge: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		return "", fmt.Errorf("decoding challenge nonce: %w", err)
	}
	signature := ed25519.Sign(signingKey, nonce)
	return api().AuthenticateDevice(state.DeviceID, challengeID, base64.StdEncoding.EncodeToString(signature))
}

func runBackup(directory, passphrase string, exclude []string) error {
	state, err := LoadDeviceState(flagStatePath)
	if err != nil {
		return err
	}
	vaultKey, err := state.WrappedVaultKey.Unwrap(passphrase)
	if err != nil {
		return fmt.Errorf("unwrapping vault key (wrong passphrase?): %w", err)
	}

	scanned, err := backupclient.Scan(directory, exclude)
	if err != nil {
		return fmt.Errorf("scanning directory: %w", err)
	}
	fmt.Printf("scanned %d files\n", len(scanned))

	config := backupclient.DefaultChunkerConfig()
	var fileEntries []backupclient.FileEntry
	// Deduplicated across the whole run, not just within one file.
	chunkMeta := make(map[string]backupclient.ChunkMeta)
	chunkCiphertext := make(map[string][]byte)

	for _, f := range scanned {
		data, err := os.ReadFile(f.AbsolutePath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", f.AbsolutePath, err)
		}
		packed, err := backupclient.Pack(data, vaultKey, config)
		if err != nil {
			return fmt.Errorf("packing %s: %w", f.AbsolutePath, err)
		}
		for _, c := range packed.Chunks {
			if _, ok := chunkMeta[c.Hash]; !ok {
				chunkMeta[c.Hash] = backupclient.ChunkMeta{
					Nonce:          hex.EncodeToString(c.Nonce[:]),
					CiphertextSize: uint64(len(c.Ciphertext)),
				}
				chunkCiphertext[c.Hash] = c.Ciphertext
			}
		}
		fileEntries = append(fileEntries, backupclient.FileEntry{
			Path: f.RelativePath, Size: f.Size, Mode: f.Mode, Modified: f.Modified, Chunks: packed.ChunkRefs,
		})
	}

	manifest := backupclient.NewManifest(fileEntries, chunkMeta)
	hashes := manifest.ReferencedChunkHashes()
	fmt.Printf("%d distinct chunks across %d files (%d total bytes)\n", len(hashes), len(manifest.Files), manifest.TotalSize)

	deviceToken, err := authenticateDevice(state)
	if err != nil {
		return fmt.Errorf("authenticating device: %w", err)
	}

	statuses, err := api().Negotiate(deviceToken, state.DeviceID, hashes)
	if err != nil {
		return fmt.Errorf("negotiating upload: %w", err)
	}
	uploaded := 0
	for _, status := range statuses {
		if status.UploadURL == "" {
			continue // server already has this chunk — nothing to upload
		}
		ciphertext, ok := chunkCiphertext[status.Hash]
		if !ok {
			return fmt.Errorf("server asked for chunk %s we never produced", status.Hash)
		}
		if err := api().PutBytes(status.UploadURL, ciphertext); err != nil {
			return fmt.Errorf("uploading chunk %s: %w", status.Hash, err)
		}
		uploaded++
	}
	fmt.Printf("uploaded %d new chunks (%d already present)\n", uploaded, len(hashes)-uploaded)

	encryptedManifest, err := manifest.Encrypt(vaultKey)
	if err != nil {
		return fmt.Errorf("encrypting manifest: %w", err)
	}
	commitChunks := make([]CommitChunk, 0, len(hashes))
	for _, h := range hashes {
		commitChunks = append(commitChunks, CommitChunk{Hash: h, CiphertextSize: uint64(len(chunkCiphertext[h]))})
	}
	snapshotID, err := api().CommitSnapshot(deviceToken, state.DeviceID, CommitRequest{
		ManifestNonce:      base64.StdEncoding.EncodeToString(encryptedManifest.Nonce[:]),
		ManifestCiphertext: base64.StdEncoding.EncodeToString(encryptedManifest.Ciphertext),
		TotalSize:          manifest.TotalSize,
		Chunks:             commitChunks,
	})
	if err != nil {
		return fmt.Errorf("committing snapshot: %w", err)
	}

	fmt.Printf("snapshot committed: %s\n", snapshotID)
	syncVaultKey(state)
	return nil
}

// syncVaultKey makes sure the account holds this device's passphrase-wrapped
// vault key, so the dashboard can open backups from devices enrolled before
// that existed. Best effort and silent on failure: the backup already
// succeeded, and a missing sign-in just means the dashboard asks for
// device.json instead.
func syncVaultKey(state DeviceState) {
	token, err := resolveUserToken()
	if err != nil {
		return
	}
	if has, err := api().HasVaultKey(token, state.DeviceID); err != nil || has {
		return
	}
	if err := api().PutVaultKey(token, state.DeviceID, state.WrappedVaultKey); err == nil {
		fmt.Println("saved your encrypted key to your account: the dashboard can now open this backup with just your passphrase")
	}
}
