package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/app"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit and build date, and the FIPS 140-3 mode and module",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := fmt.Fprintf(
				cmd.OutOrStdout(),
				"%s\n%s\n",
				app.Version(),
				app.FIPS(),
			); err != nil {
				return fmt.Errorf("write version: %w", err)
			}
			return nil
		},
	}
}
