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
	root.AddCommand(serveCmd(), schedulerCmd(), allCmd(), migrateCmd(), versionCmd())
	err := root.ExecuteContext(stopOnSignal())
	if err != nil {
		fmt.Fprintln(os.Stderr, "infraweave:", err)
		os.Exit(1)
	}
}

// stopOnSignal is a context the first SIGTERM or SIGINT cancels, which
// starts the graceful shutdown; a second exits 1 at once, for an operator
// who will not wait for the drain. SIGTERM is how a container runtime asks
// the process to stop.
func stopOnSignal() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
		<-sig
		fmt.Fprintln(os.Stderr, "infraweave: second signal, exiting without draining")
		os.Exit(1)
	}()
	return ctx
}
