package intake

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
)

func TestUnitReconcileContinuation(t *testing.T) {
	f := newFixture(t)
	for range ReconcileBatch + 1 {
		f.store.refs = append(f.store.refs, Ref{Account: f.store.in.Account, ID: kernel.NewULID()})
	}
	last := f.store.refs[ReconcileBatch]
	f.store.d = Delivery{Account: last.Account, ID: last.ID, State: Received}
	f.jobs.dead = map[string]bool{last.ID.String(): true}
	if err := f.svc.Reconciled(t.Context(), queue.Job{}); err != nil {
		t.Fatal(err)
	}
	if f.jobs.checked != ReconcileBatch || f.jobs.enqueued != 1 || f.store.d.State != Received {
		t.Fatal("first batch did not stop/continue")
	}
	var c cursor
	if json.Unmarshal(f.jobs.payload, &c) != nil || c.After != f.store.refs[ReconcileBatch-1].ID {
		t.Fatal("wrong durable continuation cursor")
	}
	if err := f.svc.Reconciled(t.Context(), queue.Job{Payload: f.jobs.payload}); err != nil {
		t.Fatal(err)
	}
	if f.jobs.checked != ReconcileBatch+1 || f.store.d.State != Failed || f.store.d.LastError != exhausted || f.jobs.complete != 2 {
		t.Fatal("later dead row starved or not completed")
	}
}

func TestUnitReconcileFailures(t *testing.T) {
	for _, fault := range []string{"payload", "pending", "dead", "state", "enqueue", "complete"} {
		t.Run(fault, func(t *testing.T) {
			f := newFixture(t)
			id := kernel.NewULID()
			f.store.refs = []Ref{{Account: f.store.in.Account, ID: id}}
			f.store.d.State = Received
			f.jobs.dead = map[string]bool{id.String(): true}
			j := queue.Job{}
			switch fault {
			case "payload":
				j.Payload = []byte(`{`)
			case "pending":
				f.store.pending = errDown
			case "dead":
				f.jobs.deadError = errDown
			case "state":
				f.store.state = errDown
			case "enqueue":
				f.jobs.enqueue = errDown
				for len(f.store.refs) < ReconcileBatch {
					f.store.refs = append(f.store.refs, Ref{Account: f.store.in.Account, ID: kernel.NewULID()})
				}
			case "complete":
				f.jobs.finish = errDown
			}
			if err := f.svc.Reconciled(t.Context(), j); err == nil {
				t.Fatal("failure ignored")
			}
			if strings.Contains(f.store.d.LastError, "secret") {
				t.Fatal("unsafe dead metadata")
			}
		})
	}
	f := newFixture(t)
	if err := f.svc.Reconciled(t.Context(), queue.Job{}); err != nil || f.jobs.complete != 1 {
		t.Fatal("empty sweep not completed", err)
	}
}
