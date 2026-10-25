package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
)

func runUsers(ctx context.Context, d *deps, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: deepctl-admin users <list|show|set-quota|set-retention|suspend|activate|deactivate|set-role> ...")
	}
	switch args[0] {
	case "list":
		return usersList(ctx, d, args[1:])
	case "show":
		return usersShow(ctx, d, args[1:])
	case "set-quota":
		return usersSetQuota(ctx, d, args[1:])
	case "set-retention":
		return usersSetRetention(ctx, d, args[1:])
	case "suspend":
		return usersSetStatus(ctx, d, args[1:], "suspended")
	case "activate":
		return usersSetStatus(ctx, d, args[1:], "active")
	case "deactivate":
		return usersSetStatus(ctx, d, args[1:], "deactivated")
	case "set-role":
		return usersSetRole(ctx, d, args[1:])
	default:
		return fmt.Errorf("unknown users subcommand: %s", args[0])
	}
}

func usersList(ctx context.Context, d *deps, args []string) error {
	limit, offset, _ := parsePageFlags(args)
	users, err := d.identity.AdminListUsers(ctx, limit, offset)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "EMAIL\tSTATUS\tPLATFORM ROLE\tQUOTA\tRETENTION\tCREATED")
	for _, u := range users {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\n",
			u.Email, u.Status, u.PlatformRole, u.StorageQuotaBytes, u.RetentionKeepSnapshots, u.CreatedAt.Format("2006-01-02"))
	}
	return tw.Flush()
}

func usersShow(ctx context.Context, d *deps, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: deepctl-admin users show <email>")
	}
	u, err := d.identity.AdminGetUserByEmail(ctx, args[0])
	if err != nil {
		return err
	}
	fmt.Printf("ID:                       %s\n", u.ID)
	fmt.Printf("Email:                    %s\n", u.Email)
	fmt.Printf("Display name:             %s\n", u.DisplayName)
	fmt.Printf("Status:                   %s\n", u.Status)
	fmt.Printf("Platform role:            %s\n", u.PlatformRole)
	fmt.Printf("Storage quota (bytes):    %d\n", u.StorageQuotaBytes)
	fmt.Printf("Retention (keep N):       %d\n", u.RetentionKeepSnapshots)
	fmt.Printf("Created:                  %s\n", u.CreatedAt)
	return nil
}

func usersSetQuota(ctx context.Context, d *deps, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: deepctl-admin users set-quota <email> <bytes>")
	}
	bytes, err := parseInt64Arg("bytes", args[1])
	if err != nil {
		return err
	}
	u, err := d.identity.AdminGetUserByEmail(ctx, args[0])
	if err != nil {
		return err
	}
	if err := d.identity.AdminSetStorageQuota(ctx, u.ID, bytes); err != nil {
		return err
	}
	fmt.Printf("%s storage quota set to %d bytes\n", u.Email, bytes)
	return nil
}

func usersSetRetention(ctx context.Context, d *deps, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: deepctl-admin users set-retention <email> <keep>")
	}
	keep, err := parseInt32Arg("keep", args[1])
	if err != nil {
		return err
	}
	u, err := d.identity.AdminGetUserByEmail(ctx, args[0])
	if err != nil {
		return err
	}
	if err := d.identity.AdminSetRetentionLimit(ctx, u.ID, keep); err != nil {
		return err
	}
	fmt.Printf("%s retention limit set to keep %d snapshots per device\n", u.Email, keep)
	return nil
}

func usersSetStatus(ctx context.Context, d *deps, args []string, status string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: deepctl-admin users %s <email>", statusVerb(status))
	}
	u, err := d.identity.AdminGetUserByEmail(ctx, args[0])
	if err != nil {
		return err
	}
	if err := d.identity.AdminSetStatus(ctx, u.ID, status); err != nil {
		return err
	}
	fmt.Printf("%s status set to %s\n", u.Email, status)
	return nil
}

// usersSetRole changes a user's platform-wide role (none/operator/admin)
// — the same action the web admin panel's "platform role" control
// performs, exposed here too so the very first admin account can be
// promoted without going through the panel (which requires already
// being an admin to use).
func usersSetRole(ctx context.Context, d *deps, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: deepctl-admin users set-role <email> <none|operator|admin>")
	}
	u, err := d.identity.AdminGetUserByEmail(ctx, args[0])
	if err != nil {
		return err
	}
	if err := d.identity.AdminSetPlatformRole(ctx, u.ID, u.ID, args[1]); err != nil {
		return err
	}
	fmt.Printf("%s platform role set to %s\n", u.Email, args[1])
	return nil
}

func statusVerb(status string) string {
	switch status {
	case "active":
		return "activate"
	case "suspended":
		return "suspend"
	default:
		return "deactivate"
	}
}
