// Command dataview writes docs/implementation/tiers.md and
// internal/features/catalogue.gen.go from the docs/data registers (make
// generate). Logic lives in internal/devtools/dataview.
package main

import (
	"fmt"
	"os"

	"github.com/weaveplatform/infraweave/internal/devtools/dataview"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr,
			"usage: dataview <docs/data> <path/to/tiers.md> <path/to/catalogue.gen.go>")
		os.Exit(2)
	}
	if err := dataview.Write(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := dataview.WriteCatalogue(os.Args[1], os.Args[3]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("dataview: wrote %s and %s\n", os.Args[2], os.Args[3])
}
