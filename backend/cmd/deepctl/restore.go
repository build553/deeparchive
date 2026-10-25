package main

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"deeparchive/internal/backupclient"
)

func newRestoreCmd() *cobra.Command {
	var passphrase, snapshotID, to string
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a snapshot's files into a target directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return fmt.Errorf("--to is required: the directory to restore into")
			}
			pass, err := passphraseOrPrompt(passphrase, false)
			if err != nil {
				return err
			}
			return runRestore(pass, snapshotID, to)
		},
	}
	cmd.Flags().StringVar(&passphrase, "passphrase", "", "Vault passphrase (default: asked securely on the terminal)")
	cmd.Flags().StringVar(&snapshotID, "snapshot", "", "Snapshot ID (default: the most recent one; see the list command)")
	cmd.Flags().StringVar(&to, "to", "", "Directory to restore into")
	return cmd
}

func runRestore(passphrase, snapshotID, to string) error {
	state, err := LoadDeviceState(flagStatePath)
	if err != nil {
		return err
	}
	vaultKey, err := state.WrappedVaultKey.Unwrap(passphrase)
	if err != nil {
		return fmt.Errorf("unwrapping vault key (wrong passphrase?): %w", err)
	}

	userToken, err := resolveUserToken()
	if err != nil {
		return err
	}
	if snapshotID == "" {
		snapshots, err := api().ListBackups(userToken)
		if err != nil {
			return err
		}
		if len(snapshots) == 0 {
			return fmt.Errorf("no snapshots to restore yet — run deepctl backup first")
		}
		latest := snapshots[0]
		for _, s := range snapshots[1:] {
			if s.CreatedAt > latest.CreatedAt { // RFC 3339 UTC sorts lexically
				latest = s
			}
		}
		snapshotID = latest.ID
		fmt.Printf("restoring most recent snapshot %s (%s)\n", latest.ID, latest.CreatedAt)
	}
	detail, err := api().GetSnapshot(userToken, snapshotID)
	if err != nil {
		return err
	}

	manifestNonceBytes, err := base64.StdEncoding.DecodeString(detail.ManifestNonce)
	if err != nil || len(manifestNonceBytes) != backupclient.NonceLen {
		return fmt.Errorf("manifest nonce from server has the wrong length")
	}
	var manifestNonce [backupclient.NonceLen]byte
	copy(manifestNonce[:], manifestNonceBytes)
	manifestCiphertext, err := base64.StdEncoding.DecodeString(detail.ManifestCiphertext)
	if err != nil {
		return fmt.Errorf("decoding manifest ciphertext: %w", err)
	}

	manifest, err := (backupclient.EncryptedManifest{Nonce: manifestNonce, Ciphertext: manifestCiphertext}).Decrypt(vaultKey)
	if err != nil {
		return fmt.Errorf("decrypting manifest (wrong passphrase, or this vault didn't create this snapshot): %w", err)
	}

	fmt.Printf("restoring %d files (%d bytes) into %s\n", len(manifest.Files), manifest.TotalSize, to)

	chunkPlaintexts := make(map[string][]byte, len(detail.Chunks))
	for _, loc := range detail.Chunks {
		meta, ok := manifest.Chunks[loc.Hash]
		if !ok {
			return fmt.Errorf("server listed chunk %s that the manifest never references", loc.Hash)
		}
		nonceBytes, err := hex.DecodeString(meta.Nonce)
		if err != nil || len(nonceBytes) != backupclient.NonceLen {
			return fmt.Errorf("chunk %s has invalid nonce encoding", loc.Hash)
		}
		var nonce [backupclient.NonceLen]byte
		copy(nonce[:], nonceBytes)

		ciphertext, err := api().GetBytes(loc.DownloadURL)
		if err != nil {
			return fmt.Errorf("downloading chunk %s: %w", loc.Hash, err)
		}
		plaintext, err := backupclient.Decrypt(vaultKey, nonce, ciphertext, []byte(loc.Hash))
		if err != nil {
			return fmt.Errorf("decrypting chunk %s: %w", loc.Hash, err)
		}
		chunkPlaintexts[loc.Hash] = plaintext
	}

	if err := os.MkdirAll(to, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", to, err)
	}
	for _, file := range manifest.Files {
		dest, err := safeJoin(to, file.Path)
		if err != nil {
			return fmt.Errorf("manifest file path %q is unsafe: %w", file.Path, err)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(dest), err)
		}
		contents, err := backupclient.Unpack(file.Chunks, chunkPlaintexts)
		if err != nil {
			return fmt.Errorf("reassembling %s: %w", file.Path, err)
		}
		if err := os.WriteFile(dest, contents, os.FileMode(file.Mode)); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}
		// Safe to call unconditionally: a no-op on platforms (Windows)
		// that don't support Unix permission bits, rather than needing
		// a separate build-tagged path like scanner.go's fileMode.
		_ = os.Chmod(dest, os.FileMode(file.Mode))
	}

	fmt.Println("restore complete")
	return nil
}

// safeJoin joins relative (forward-slash-separated, as stored in the
// manifest) onto base, rejecting anything that would escape base — an
// absolute path, a ".." component, or an empty component. A manifest is
// encrypted end-to-end and the server never validates its contents, so a
// corrupted or maliciously crafted manifest must not be able to write
// outside the chosen restore directory.
func safeJoin(base, relative string) (string, error) {
	out := base
	for _, part := range strings.Split(relative, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("path escapes the restore directory")
		default:
			out = filepath.Join(out, part)
		}
	}
	return out, nil
}
