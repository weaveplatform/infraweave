package main

import (
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func TestUnitMatrixIncludesNativeAndEveryCataloguePartitionExactlyOnce(t *testing.T) {
	jobs := matrix()["include"]
	actual := make([]string, 0, len(jobs))
	expected := make([]string, 0, 1+len(harness.Partitions))
	expected = append(expected, "native")
	for _, partition := range harness.Partitions {
		expected = append(expected, partition.Name)
	}
	for _, job := range jobs {
		actual = append(actual, job.Shard)
		if job.Shard == "native" && job.Partition != "" || job.Shard != "native" && job.Partition != job.Shard {
			t.Errorf("shard %q selects unintended partition %q", job.Shard, job.Partition)
		}
		if job.Timeout <= 60 || job.Timeout > 90 {
			t.Errorf("shard %q has unbounded or insufficient job budget %d", job.Shard, job.Timeout)
		}
	}
	if !slices.Equal(actual, expected) {
		t.Fatalf("matrix jobs=%v; need native plus complete catalogue=%v", actual, expected)
	}
}
