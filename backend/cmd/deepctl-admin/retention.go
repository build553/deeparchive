package main

import (
	"context"
	"fmt"
)

func runRetention(ctx context.Context, d *deps, args []string) error {
	if len(args) != 1 || args[0] != "sweep" {
		return fmt.Errorf("usage: deepctl-admin retention sweep")
	}
	deleted, err := d.retention.Sweep(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("retention sweep complete: %d snapshot(s) deleted\n", deleted)
	return nil
}

func runUsage(ctx context.Context, d *deps, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: deepctl-admin usage <email>")
	}
	u, err := d.identity.AdminGetUserByEmail(ctx, args[0])
	if err != nil {
		return err
	}
	report, err := d.usage.Report(ctx, u.ID)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d / %d bytes used (personal scope)\n", u.Email, report.Personal.UsedBytes, report.Personal.QuotaBytes)
	for _, o := range report.Organizations {
		fmt.Printf("  org %s (%s): %d / %d bytes used\n", o.Name, o.OrganizationID, o.UsedBytes, o.QuotaBytes)
	}
	return nil
}
