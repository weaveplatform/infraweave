package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/app"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
)

// runMode runs process mode m with its logs on stdout. SIGHUP re-reads
// log.level from the flags, the environment and the file, and nothing else.
func runMode(m app.Mode) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		c, err := config.Load(cmd.Flags())
		if err != nil {
			return err
		}
		log, level, err := observe.NewLogger(cmd.OutOrStdout(), c.Log, m.String())
		if err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		hup := make(chan os.Signal, 1)
		signal.Notify(hup, syscall.SIGHUP)
		defer signal.Stop(hup)
		go observe.FollowLevel(ctx, hup, func() (slog.Level, error) {
			c, err := config.Load(cmd.Flags())
			if err != nil {
				return 0, err
			}
			return observe.ParseLevel(c.Log.Level)
		}, level, log)
		return app.Run(ctx, *c, log, m)
	}
}
