package main

import (
	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/app"
	"github.com/weaveplatform/infraweave/internal/app/config"
)

func serveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the API on the API listener and the probes on the admin listener",
		Args:  cobra.NoArgs,
		RunE:  runMode(app.ModeServe),
	}
	config.ServeFlags(cmd.Flags())
	return cmd
}
