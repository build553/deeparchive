package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List snapshots visible to the logged-in user",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList()
		},
	}
}

func runList() error {
	userToken, err := resolveUserToken()
	if err != nil {
		return err
	}
	snapshots, err := api().ListBackups(userToken)
	if err != nil {
		return err
	}
	if len(snapshots) == 0 {
		fmt.Println("no snapshots yet")
		return nil
	}
	for _, s := range snapshots {
		fmt.Printf("%s  %s  %d bytes, %d chunks\n", s.ID, s.CreatedAt, s.TotalSize, s.ChunkCount)
	}
	return nil
}
