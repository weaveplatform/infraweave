package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/app"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit and build date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), app.Version()); err != nil {
				return fmt.Errorf("write version: %w", err)
			}
			return nil
		},
	}
}
