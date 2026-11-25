package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type shard struct {
	Shard     string `json:"shard"`
	Partition string `json:"partition"`
	Timeout   int    `json:"timeout"`
}

// jobBudget is every shard's Actions budget in minutes: the Makefile's
// sixty-minute Go package deadline for accept and accept-native, plus
// setup, compilation and coverage upload, so the Go deadline, which names
// the test still running, always fires before Actions cancels the job.
const jobBudget = 75

func matrix() map[string][]shard {
	jobs := make([]shard, 0, 1+len(harness.Partitions))
	jobs = append(jobs, shard{Shard: "native", Timeout: jobBudget})
	for _, partition := range harness.Partitions {
		jobs = append(jobs, shard{Shard: partition.Name, Partition: partition.Name, Timeout: jobBudget})
	}
	return map[string][]shard{"include": jobs}
}

func main() {
	if err := json.NewEncoder(os.Stdout).Encode(matrix()); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
