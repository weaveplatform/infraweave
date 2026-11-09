// Command vendorsfixtures records the vendors' release indexes as the
// index readers' test fixtures (make vendors-fixtures). Logic lives in
// internal/devtools/vendorsfixtures.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/weaveplatform/infraweave/internal/devtools/vendorsfixtures"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: vendorsfixtures <testdata directory>")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: time.Minute}
	today := time.Now().UTC().Format(time.DateOnly)
	if err := vendorsfixtures.Run(
		ctx,
		client,
		vendorsfixtures.Sources,
		os.Args[1],
		today,
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1) //nolint:gocritic // cancel only ends a context nothing waits on
	}
	fmt.Printf(
		"vendorsfixtures: wrote %d fixtures to %s\n",
		len(vendorsfixtures.Sources),
		os.Args[1],
	)
}
