package controller_test

import (
	"regexp"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/weaveplatform/infraweave/internal/agent/controller"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitSlotNamesFenceReplacementResourceIdentities(t *testing.T) {
	seen := map[string]bool{}
	for _, uid := range []string{"resource-one", "resource-two"} {
		for _, slot := range []int32{0, 1, 2147483647} {
			name := controller.SlotName(uid, slot)
			if len(name) > 63 || !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(name) || seen[name] {
				t.Fatalf("invalid or colliding slot name: %s", name)
			}
			if name != controller.SlotName(uid, slot) {
				t.Fatal("slot identity is not deterministic")
			}
			seen[name] = true
		}
	}
	pool := controller.WorkerPool{ObjectMeta: metav1.ObjectMeta{Name: "display-name", Namespace: "workers", UID: types.UID("opaque-resource-uid")}}
	owner := pool.Owner("cluster-one")
	pool.Name = "renamed"
	if pool.Owner("cluster-one") != owner || owner != (kernel.WorkerOwner{ClusterID: "cluster-one", Namespace: "workers", ResourceUID: "opaque-resource-uid"}) {
		t.Fatalf("display name changed ownership: %+v", owner)
	}
}
