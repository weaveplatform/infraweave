package main

import (
	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/app"
	"github.com/weaveplatform/infraweave/internal/app/config"
)

func schedulerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scheduler",
		Short: "Run the scheduler, with the probes on the admin listener",
		Args:  cobra.NoArgs,
		RunE:  runMode(app.ModeScheduler),
	}
	config.AdminFlags(cmd.Flags())
	return cmd
}
