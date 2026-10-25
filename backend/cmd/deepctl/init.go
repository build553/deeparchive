package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"deeparchive/internal/backupclient"
)

func newInitCmd() *cobra.Command {
	var name, osName, passphrase string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Register a new device and vault",
		Long: `Register a new device and vault: uses your stored sign-in (run "deepctl
login" first), generates a device keypair, enrolls and self-approves it,
generates a fresh vault key, and wraps it under --passphrase. Writes
--state.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				if h, err := os.Hostname(); err == nil {
					name = h
				} else {
					name = "my-device"
				}
			}
			if osName == "" {
				osName = runtime.GOOS
			}
			pass, err := passphraseOrPrompt(passphrase, true)
			if err != nil {
				return err
			}
			return runInit(name, osName, pass)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "A name for this device (default: this computer's hostname)")
	cmd.Flags().StringVar(&osName, "os", "", "Operating system label (default: detected)")
	cmd.Flags().StringVar(&passphrase, "passphrase", "", "Vault passphrase (default: asked securely on the terminal). Remember it: there is no recovery without it")
	return cmd
}

func runInit(name, osName, passphrase string) error {
	if _, err := os.Stat(flagStatePath); err == nil {
		return fmt.Errorf("%s already exists — remove it first if you really want to re-enroll", flagStatePath)
	}

	userToken, err := resolveUserToken()
	if err != nil {
		return err
	}

	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return fmt.Errorf("reading OS randomness: %w", err)
	}
	signingKey := ed25519.NewKeyFromSeed(seed)
	publicKeyB64 := base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey))

	vaultKey, err := backupclient.GenerateVaultKey()
	if err != nil {
		return err
	}
	wrapped, err := backupclient.WrapVaultKey(vaultKey, passphrase)
	if err != nil {
		return fmt.Errorf("wrapping vault key: %w", err)
	}

	deviceID, err := api().EnrollDevice(userToken, name, osName, version, publicKeyB64, wrapped)
	if err != nil {
		return fmt.Errorf("enrolling device: %w", err)
	}
	if err := api().ApproveDevice(userToken, deviceID); err != nil {
		return fmt.Errorf("approving device: %w", err)
	}

	state := NewDeviceState(deviceID, seed, wrapped)
	if err := state.Save(flagStatePath); err != nil {
		return err
	}

	fmt.Printf("enrolled device %s\n", deviceID)
	fmt.Printf("state written to %s\n", flagStatePath)
	fmt.Println("remember your passphrase — it is never sent to the server and there is no recovery without it")
	return nil
}
