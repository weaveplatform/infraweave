// Command dataview writes docs/implementation/tiers.md from the docs/data
// registers (make generate). Logic lives in internal/devtools/dataview.
package main

import (
	"fmt"
	"os"

	"github.com/weaveplatform/infraweave/internal/devtools/dataview"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: dataview <docs/data> <path/to/tiers.md>")
		os.Exit(2)
	}
	if err := dataview.Write(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("dataview: wrote %s\n", os.Args[2])
}
