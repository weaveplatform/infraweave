// Command sigcheck reports each Go function signature over several lines
// that fits on one line of golines' limit in .golangci.yml, and with -w
// joins them in place (make sig-check, make sig-join). Logic lives in
// internal/devtools/sigcheck.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/weaveplatform/infraweave/internal/devtools/sigcheck"
)

func main() {
	write := flag.Bool("w", false, "join each signature that fits, in place")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: sigcheck [-w] [root]")
		flag.PrintDefaults()
	}
	flag.Parse()
	root := "."
	if flag.NArg() > 1 {
		flag.Usage()
		os.Exit(2)
	}
	if flag.NArg() == 1 {
		root = flag.Arg(0)
	}
	limit, err := sigcheck.ReadLimit(filepath.Join(root, ".golangci.yml"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	found, err := sigcheck.Walk(root, limit, *write)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *write {
		fmt.Printf("sigcheck: joined %d signatures onto one line of %d\n", len(found), limit.MaxLen)
		return
	}
	for _, w := range found {
		fmt.Fprintln(os.Stderr, w)
	}
	if len(found) > 0 {
		fmt.Fprintf(os.Stderr, "sigcheck: %d signatures fit on one line of %d; make sig-join joins them\n",
			len(found), limit.MaxLen)
		os.Exit(1)
	}
}
