// Command stubgen writes unimplemented.gen.go next to a generated strict
// server (make generate). Logic lives in internal/stubgen.
package main

import (
	"fmt"
	"os"

	"github.com/weaveplatform/infraweave/internal/stubgen"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(
			os.Stderr,
			"usage: stubgen <path/to/server.gen.go> <path/to/spec.openapi.yaml>",
		)
		os.Exit(2)
	}
	n, err := stubgen.Run(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("stubgen: %d operations stubbed in %s\n", n, os.Args[1])
}
