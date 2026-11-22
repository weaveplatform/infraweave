package kernel_test

import (
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitWorkerOwnerIdentity(t *testing.T) {
	valid := kernel.WorkerOwner{ClusterID: "build-cluster-1", Namespace: "workers", ResourceUID: "d74c2359-bb7e-4bb7-a181-e88dfac151c6"}
	if faults := kernel.CheckWorkerOwner("/management", valid); len(faults) != 0 {
		t.Fatalf("valid Kubernetes UID rejected: %+v", faults)
	}
	for _, tt := range []struct{ name, field, value string }{
		{"missing cluster", "clusterID", ""},
		{"cluster case", "clusterID", "Cluster"},
		{"cluster suffix", "clusterID", "cluster-"},
		{"cluster prefix", "clusterID", "-cluster"},
		{"cluster length", "clusterID", strings.Repeat("a", 64)},
		{"cluster path", "clusterID", "cluster/name"},
		{"namespace dot", "namespace", "workers.example"},
		{"namespace underscore", "namespace", "worker_pool"},
		{"namespace space", "namespace", "workers "},
		{"missing UID", "resourceUID", ""},
		{"blank UID", "resourceUID", " "},
		{"long UID", "resourceUID", strings.Repeat("u", 129)},
		{"invalid UID UTF8", "resourceUID", string([]byte{0xff})},
		{"control UID", "resourceUID", "uid\nother"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			owner := valid
			switch tt.field {
			case "clusterID":
				owner.ClusterID = tt.value
			case "namespace":
				owner.Namespace = tt.value
			case "resourceUID":
				owner.ResourceUID = tt.value
			}
			faults := kernel.CheckWorkerOwner("/management", owner)
			if len(faults) != 1 || faults[0].Pointer != "/management/"+tt.field {
				t.Fatalf("identity faults: %+v", faults)
			}
		})
	}
	maximum := kernel.WorkerOwner{ClusterID: strings.Repeat("a", 63), Namespace: strings.Repeat("n", 63), ResourceUID: strings.Repeat("u", 128)}
	if faults := kernel.CheckWorkerOwner("/management", maximum); len(faults) != 0 {
		t.Fatalf("boundary identity rejected: %+v", faults)
	}
}
