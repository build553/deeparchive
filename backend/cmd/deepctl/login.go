package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	var clientName string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in via your browser (no password typed into this terminal)",
		Long: `Sign in via your browser — the same device-code flow "gh auth login" uses.
No password is ever typed into this terminal or saved anywhere; only an
access/refresh token pair is, in --credentials. Run this once per
machine, then every other command picks up the stored sign-in
automatically.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return login(clientName)
		},
	}
	cmd.Flags().StringVar(&clientName, "client-name", "deepctl", "Shown on the approval page (\"<name> wants to sign in\")")
	return cmd
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Forget the locally stored sign-in",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(flagCredsPath); err == nil {
				if err := os.Remove(flagCredsPath); err != nil {
					return fmt.Errorf("removing %s: %w", flagCredsPath, err)
				}
				fmt.Println("logged out")
			} else {
				fmt.Println("already logged out")
			}
			return nil
		},
	}
}

// login runs the device-code flow: print a short code and a URL, try to
// open the URL in the person's browser, then poll until they approve it
// there — identical shape to `gh auth login`.
func login(clientName string) error {
	start, err := api().DeviceCode(clientName)
	if err != nil {
		return fmt.Errorf("starting sign-in: %w", err)
	}

	fmt.Printf("First copy your one-time code: %s\n\n", start.UserCode)
	fmt.Printf("Then open this URL in your browser to confirm:\n  %s\n\n", start.VerificationURIComplete)
	_ = openBrowser(start.VerificationURIComplete) // best-effort
	fmt.Print("Waiting for confirmation in the browser...")

	interval := time.Duration(start.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		time.Sleep(interval)
		result, err := api().DeviceToken(start.DeviceCode)
		if err != nil {
			fmt.Println()
			return fmt.Errorf("checking sign-in status: %w", err)
		}
		switch {
		case result.Session != nil:
			fmt.Println()
			creds := UserCredentials{AccessToken: result.Session.AccessToken, RefreshToken: result.Session.RefreshToken, Server: resolveServer()}
			if err := creds.Save(flagCredsPath); err != nil {
				return err
			}
			fmt.Printf("logged in — credentials saved to %s\n", flagCredsPath)
			return nil
		case result.SlowDown:
			interval += 5 * time.Second
		case result.Denied:
			fmt.Println()
			return fmt.Errorf("sign-in was denied")
		case result.Expired:
			fmt.Println()
			return fmt.Errorf("code expired before it was approved — run `deepctl login` again")
		default: // Pending
			fmt.Print(".")
		}
	}
}

// openBrowser is best-effort: a browser that doesn't open is not a
// failure, just a nudge to copy the URL that's already been printed.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("cmd", "/C", "start", "", url).Start()
	default:
		return fmt.Errorf("no known way to open a browser on %s", runtime.GOOS)
	}
}
