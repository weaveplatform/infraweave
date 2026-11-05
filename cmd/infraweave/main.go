// Command infraweave runs one process mode of infraweave, one cobra file per
// mode over internal/app.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:           "infraweave",
		Short:         "infraweave serves an infrastructure-as-code orchestration API",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(serveCmd(), migrateCmd(), versionCmd())
	// SIGTERM is how a container runtime asks the process to stop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := root.ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "infraweave:", err)
		os.Exit(1)
	}
}
