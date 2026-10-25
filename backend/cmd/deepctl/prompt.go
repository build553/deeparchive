package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// passphraseOrPrompt returns flagValue if given (scripts/CI); otherwise
// asks on the terminal with echo off, so the passphrase never lands in
// shell history or the process list the way a --passphrase flag does.
// confirm asks twice — for `init`, where a typo would silently create a
// vault nobody can open.
func passphraseOrPrompt(flagValue string, confirm bool) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if v := os.Getenv("DEEPARCHIVE_PASSPHRASE"); v != "" {
		return v, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("no terminal to ask for the passphrase: pass --passphrase or set DEEPARCHIVE_PASSPHRASE")
	}
	first, err := readSecret("Vault passphrase: ")
	if err != nil {
		return "", err
	}
	if first == "" {
		return "", fmt.Errorf("passphrase cannot be empty")
	}
	if confirm {
		second, err := readSecret("Confirm passphrase: ")
		if err != nil {
			return "", err
		}
		if first != second {
			return "", fmt.Errorf("passphrases did not match")
		}
	}
	return first, nil
}

func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
