package main

import (
	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/app"
	"github.com/weaveplatform/infraweave/internal/app/config"
)

func allCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "all",
		Short: "Run serve and scheduler in one process",
		Args:  cobra.NoArgs,
		RunE:  runMode(app.ModeAll),
	}
	config.ServeFlags(cmd.Flags())
	return cmd
}
