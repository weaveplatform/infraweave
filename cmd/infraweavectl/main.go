// Command infraweavectl is infraweave's client CLI, the command tree of
// internal/ctl over pkg/sdk (decision 0001).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/weaveplatform/infraweave/internal/ctl"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := ctl.Root().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "infraweavectl:", err)
		os.Exit(1)
	}
}
