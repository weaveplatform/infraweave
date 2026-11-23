// Command workerchart writes the infraweave-workers chart's values schema
// from its WorkerPool CRD (make generate), or with -check fails when the
// committed schema is stale. Logic lives in internal/devtools/workerchart.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/weaveplatform/infraweave/internal/devtools/workerchart"
)

func main() {
	check := flag.Bool("check", false, "fail when the committed schema is stale instead of writing it")
	chart := flag.String("chart", "deploy/helm/infraweave-workers", "the chart directory")
	flag.Parse()
	run := workerchart.Write
	if *check {
		run = workerchart.Check
	}
	if err := run(*chart); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
