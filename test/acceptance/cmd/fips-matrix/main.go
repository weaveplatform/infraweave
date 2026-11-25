package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type shard struct {
	Shard     string `json:"shard"`
	Partition string `json:"partition"`
	Timeout   int    `json:"timeout"`
}

func matrix() map[string][]shard {
	jobs := make([]shard, 0, 1+len(harness.Partitions))
	jobs = append(jobs, shard{Shard: "native", Timeout: 20})
	for _, partition := range harness.Partitions {
		budget := 20
		if strings.HasPrefix(partition.Name, "terraform") || partition.Name == "runs" || partition.Name == "run-credentials" {
			budget = 30
		}
		jobs = append(jobs, shard{Shard: partition.Name, Partition: partition.Name, Timeout: budget})
	}
	return map[string][]shard{"include": jobs}
}

func main() {
	if err := json.NewEncoder(os.Stdout).Encode(matrix()); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
