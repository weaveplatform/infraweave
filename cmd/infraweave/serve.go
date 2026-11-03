package main

import (
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/app"
	"github.com/weaveplatform/infraweave/internal/server"
)

func serveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the deploy API on the API listener and the probes on the admin listener",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := app.Load(cmd.Flags())
			if err != nil {
				return err
			}
			log := slog.New(slog.NewJSONHandler(cmd.ErrOrStderr(), nil))
			return server.Run(cmd.Context(), c.Server, log)
		},
	}
	app.ServeFlags(cmd.Flags())
	return cmd
}
