// Command deepctl is the end-user DeepArchive CLI: enroll a device, back
// up a directory, list snapshots, and restore one — everything a normal
// account holder does, over the same HTTP API the web dashboard uses.
// Deliberately a separate binary from deepctl-admin (backend/cmd/deepctl-admin,
// the platform-operator tool with direct database access) — see that
// command's doc comment for why conflating the two "admin" concepts
// (platform admin vs. an organization's own admin role, which this CLI
// reaches through normal HTTP sessions like any other member) was worth
// avoiding.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// version is overwritten at build time via -ldflags "-X main.version=...".
// See .github/workflows/release.yml.
var version = "dev"

// defaultServer is what deepctl talks to when nothing else says
// otherwise. Release builds bake in the real hosted URL via
// -ldflags "-X main.defaultServer=https://..." (see
// .github/workflows/release.yml); a plain `go build` falls back to the
// local dev stack.
var defaultServer = "http://localhost:8080"

var (
	flagServer    string
	flagStatePath string
	flagCredsPath string
	flagResolve   []string
)

func main() {
	root := &cobra.Command{
		Use:          "deepctl",
		Short:        "DeepArchive end-user CLI",
		Version:      version,
		SilenceUsage: true,
	}
	root.PersistentFlags().StringVar(&flagServer, "server", "", "Override the API base URL (also env DEEPARCHIVE_SERVER). Normally not needed: login remembers the server")
	// Note: no backticks in these descriptions — pflag reads a
	// backtick-quoted substring as the flag's displayed value-type name
	// (e.g. "--state init" instead of "--state string"), it isn't
	// markdown.
	root.PersistentFlags().StringVar(&flagStatePath, "state", configPath("device.json"), "Path to the local device state file written by the init command")
	root.PersistentFlags().StringVar(&flagCredsPath, "credentials", configPath("credentials.json"), "Path to the local sign-in state file written by the login command")
	root.PersistentFlags().StringArrayVar(&flagResolve, "resolve", nil, "Override DNS resolution for HOST:IP:PORT (repeatable), like curl's --resolve. Needed for local Docker Compose dev only: a presigned storage URL embeds the compose-internal hostname, (automatic when the server is localhost)")

	root.AddCommand(newLoginCmd())
	root.AddCommand(newLogoutCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newBackupCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newRestoreCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// configPath puts deepctl's state in the per-user config directory
// (~/.config/deeparchive on Linux, ~/Library/Application Support/deeparchive
// on macOS, %AppData%\\deeparchive on Windows) so commands work from any
// directory instead of only the one `init` happened to run in. Falls back
// to the current directory if the OS reports no config dir.
func configPath(name string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "deeparchive-" + name
	}
	return filepath.Join(dir, "deeparchive", name)
}

// resolveServer picks the API base URL: --server, then
// DEEPARCHIVE_SERVER, then the server remembered by `login`, then the
// built-in default.
func resolveServer() string {
	if flagServer != "" {
		return strings.TrimRight(flagServer, "/")
	}
	if env := os.Getenv("DEEPARCHIVE_SERVER"); env != "" {
		return strings.TrimRight(env, "/")
	}
	if creds, err := LoadUserCredentials(flagCredsPath); err == nil && creds.Server != "" {
		return creds.Server
	}
	if defaultServer == "" {
		return "http://localhost:8080"
	}
	return strings.TrimRight(defaultServer, "/")
}

func api() *API {
	overrides := make(map[string]string, len(flagResolve))
	for _, spec := range flagResolve {
		dialKey, target, ok := parseResolve(spec)
		if !ok {
			fmt.Fprintf(os.Stderr, "warning: ignoring malformed --resolve %q (want HOST:IP:PORT)\n", spec)
			continue
		}
		overrides[dialKey] = target
	}
	return NewAPI(resolveServer(), overrides)
}

// parseResolve splits "HOST:IP:PORT" (e.g. "minio:127.0.0.1:9000") into
// the dial key Go's http.Transport.DialContext actually requests
// ("minio:9000" — host and port exactly as embedded in the URL being
// fetched) and the real target to dial instead ("127.0.0.1:9000"). Only
// the host is overridden, same as curl's --resolve or a /etc/hosts
// entry — the port must still be given explicitly since it has to
// appear on both sides of the mapping.
func parseResolve(spec string) (dialKey, target string, ok bool) {
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	host, ip, port := parts[0], parts[1], parts[2]
	return host + ":" + port, ip + ":" + port, true
}

// resolveUserToken returns a valid user access token from the stored
// login, refreshing it first — see Api.Refresh's doc comment for why
// this always refreshes rather than checking the access token's expiry
// client-side. Every command needing a user session (not a device
// session) goes through this.
func resolveUserToken() (string, error) {
	creds, err := LoadUserCredentials(flagCredsPath)
	if err != nil {
		return "", fmt.Errorf("not logged in — run `deepctl login` first")
	}
	session, err := api().Refresh(creds.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("stored sign-in is no longer valid — run `deepctl login` again: %w", err)
	}
	newCreds := UserCredentials{AccessToken: session.AccessToken, RefreshToken: session.RefreshToken, Server: creds.Server}
	if err := newCreds.Save(flagCredsPath); err != nil {
		return "", err
	}
	return session.AccessToken, nil
}
