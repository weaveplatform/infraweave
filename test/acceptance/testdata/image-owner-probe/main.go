// Command image-owner-probe reports the actual user and workspace of a test
// container. It is statically built for the real Docker daemon's architecture.
package main

import (
	"encoding/json"
	"os"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		UID int    `json:"uid"`
		Dir string `json:"dir"`
	}{os.Getuid(), dir}); err != nil {
		os.Exit(1)
	}
}
