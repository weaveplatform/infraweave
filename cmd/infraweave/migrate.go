package main

import (
	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/app"
	"github.com/weaveplatform/infraweave/internal/app/config"
)

func migrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply or list the schema migrations, for operators who migrate in a separate job",
		Args:  cobra.NoArgs,
	}
	run := func(f func(*cobra.Command, config.Config) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			c, err := config.Load(cmd.Flags())
			if err != nil {
				return err
			}
			return f(cmd, *c)
		}
	}
	up := &cobra.Command{
		Use:   "up",
		Short: "Apply every pending migration and exit",
		Args:  cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, c config.Config) error {
			return app.MigrateUp(cmd.Context(), c, cmd.OutOrStdout())
		}),
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "List each stream's applied and pending migrations",
		Args:  cobra.NoArgs,
		RunE: run(func(cmd *cobra.Command, c config.Config) error {
			return app.MigrateStatus(cmd.Context(), c, cmd.OutOrStdout())
		}),
	}
	config.FileFlag(up.Flags())
	config.FileFlag(status.Flags())
	cmd.AddCommand(up, status)
	return cmd
}
