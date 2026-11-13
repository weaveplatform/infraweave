// Command planfixtures records Terraform's and OpenTofu's plans as
// internal/iac/plan's test fixtures (make iac-fixtures). Logic lives in
// test/planfixtures.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/weaveplatform/infraweave/test/deps"
	"github.com/weaveplatform/infraweave/test/planfixtures"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: planfixtures <testdata directory>")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var d deps.Dependencies
	err := planfixtures.Record(ctx, &d, planfixtures.Tools, planfixtures.Scenarios, os.Args[1])
	if cerr := d.Close(ctx); err == nil {
		err = cerr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1) //nolint:gocritic // cancel only ends a context nothing waits on
	}
	fmt.Printf("planfixtures: wrote %d plans of %d tools to %s\n",
		len(planfixtures.Scenarios), len(planfixtures.Tools), os.Args[1])
}
