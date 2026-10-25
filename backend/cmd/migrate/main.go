// Command migrate applies or rolls back database migrations using
// golang-migrate against DATABASE_URL. It is a thin wrapper so `make
// migrate-up` / `make migrate-down` do not require developers to install
// the golang-migrate CLI separately, and so CI can run migrations without a
// host install step.
package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: migrate <up|down|version> [steps]")
		os.Exit(2)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(2)
	}

	m, err := migrate.New("file://db/migrations", dbURL)
	if err != nil {
		fatal(err)
	}

	switch os.Args[1] {
	case "up":
		if steps, ok := optionalSteps(); ok {
			err = m.Steps(steps)
		} else {
			err = m.Up()
		}
	case "down":
		// Defaults to rolling back exactly one migration — "down" meaning
		// "all the way to empty" is rarely what a developer wants and has
		// destroyed real schema state in other projects when run by habit.
		// Pass an explicit step count (e.g. `migrate down 3`) for more.
		steps, ok := optionalSteps()
		if !ok {
			steps = -1
		}
		err = m.Steps(steps)
	case "version":
		v, dirty, verr := m.Version()
		if verr != nil {
			fatal(verr)
		}
		fmt.Printf("version=%d dirty=%v\n", v, dirty)
		return
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", os.Args[1])
		os.Exit(2)
	}

	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		fatal(err)
	}
	fmt.Println("ok")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "migrate error:", err)
	os.Exit(1)
}

// optionalSteps parses an optional positive step count from os.Args[2] and
// returns it negated for "down" (golang-migrate's Steps takes negative N to
// go backwards). ok is false when no step count was given.
func optionalSteps() (steps int, ok bool) {
	if len(os.Args) < 3 {
		return 0, false
	}
	n, err := strconv.Atoi(os.Args[2])
	if err != nil || n <= 0 {
		fmt.Fprintln(os.Stderr, "steps must be a positive integer")
		os.Exit(2)
	}
	if os.Args[1] == "down" {
		return -n, true
	}
	return n, true
}
