package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/google/uuid"
)

func runOrgs(ctx context.Context, d *deps, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: deepctl-admin orgs <list|show|set-quota|set-retention> ...")
	}
	switch args[0] {
	case "list":
		return orgsList(ctx, d, args[1:])
	case "show":
		return orgsShow(ctx, d, args[1:])
	case "set-quota":
		return orgsSetQuota(ctx, d, args[1:])
	case "set-retention":
		return orgsSetRetention(ctx, d, args[1:])
	default:
		return fmt.Errorf("unknown orgs subcommand: %s", args[0])
	}
}

func orgsList(ctx context.Context, d *deps, args []string) error {
	limit, offset, _ := parsePageFlags(args)
	orgs, err := d.orgs.AdminListOrganizations(ctx, limit, offset)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSLUG\tQUOTA\tRETENTION\tCREATED")
	for _, o := range orgs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\n",
			o.ID, o.Name, o.Slug, o.StorageQuotaBytes, o.RetentionKeepSnapshots, o.CreatedAt.Format("2006-01-02"))
	}
	return tw.Flush()
}

func orgsShow(ctx context.Context, d *deps, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: deepctl-admin orgs show <org-id>")
	}
	id, err := uuid.Parse(args[0])
	if err != nil {
		return fmt.Errorf("invalid org id: %w", err)
	}
	o, err := d.orgs.AdminGetByID(ctx, id)
	if err != nil {
		return err
	}
	fmt.Printf("ID:                       %s\n", o.ID)
	fmt.Printf("Name:                     %s\n", o.Name)
	fmt.Printf("Slug:                     %s\n", o.Slug)
	fmt.Printf("Owner ID:                 %s\n", o.OwnerID)
	fmt.Printf("Storage quota (bytes):    %d\n", o.StorageQuotaBytes)
	fmt.Printf("Retention (keep N):       %d\n", o.RetentionKeepSnapshots)
	fmt.Printf("Created:                  %s\n", o.CreatedAt)
	return nil
}

func orgsSetQuota(ctx context.Context, d *deps, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: deepctl-admin orgs set-quota <org-id> <bytes>")
	}
	id, err := uuid.Parse(args[0])
	if err != nil {
		return fmt.Errorf("invalid org id: %w", err)
	}
	bytes, err := parseInt64Arg("bytes", args[1])
	if err != nil {
		return err
	}
	if err := d.orgs.AdminSetStorageQuota(ctx, id, bytes); err != nil {
		return err
	}
	fmt.Printf("organization %s storage quota set to %d bytes\n", id, bytes)
	return nil
}

func orgsSetRetention(ctx context.Context, d *deps, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: deepctl-admin orgs set-retention <org-id> <keep>")
	}
	id, err := uuid.Parse(args[0])
	if err != nil {
		return fmt.Errorf("invalid org id: %w", err)
	}
	keep, err := parseInt32Arg("keep", args[1])
	if err != nil {
		return err
	}
	if err := d.orgs.AdminSetRetentionLimit(ctx, id, keep); err != nil {
		return err
	}
	fmt.Printf("organization %s retention limit set to keep %d snapshots per device\n", id, keep)
	return nil
}
