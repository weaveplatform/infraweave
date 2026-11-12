package assembly

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestUnitDomain checks that the domain answers the stack view's status
// and the stacks guard from one service, declares its stream, held
// columns, key and sealed columns, binds the expression registry its handover renders
// with, and that a nil domain's partial is the zero one.
func TestUnitDomain(t *testing.T) {
	d := New(Deps{})
	if d.StackStatus() == nil || d.StackGuard() == nil ||
		any(d.StackStatus()) != any(d.StackGuard()) {
		t.Error("the status and the guard are not the domain's service")
	}
	if Stream().Name != pg.StreamName || !reflect.DeepEqual(Held(), pg.Held()) ||
		!reflect.DeepEqual(Keys(), pg.Keys()) ||
		!reflect.DeepEqual(SealedColumns(), pg.SealedColumns()) {
		t.Errorf("stream %q, held %v, keys %v, sealed %v", Stream().Name, Held(), Keys(),
			SealedColumns())
	}
	if d.Scheduler() == nil || d.Liveness() == nil {
		t.Error("the domain has no scheduler or liveness")
	}
	d.BindExpressions(nil)
	var none *Domain
	if !reflect.ValueOf(none.API()).IsZero() {
		t.Error("a nil domain's partial is not the zero one")
	}
}

// sources is a runsapi.RunSources that releases nothing and a
// stacksapi.StackSources that resolves every ref to the zero commit.
type sources struct{}

func (sources) Release(context.Context, kernel.AccountID, kernel.ULID) error { return nil }

func (sources) Resolve(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
	string,
) (stacksapi.Commit, error) {
	return stacksapi.Commit{}, nil
}

// TestUnitEvents checks that the domain's event kind registers once, that
// a run_updated document meets its schema, that the vcs domain's back edge
// binds, and that the domain hands over its run.schedule handler and
// subscribes its consumer once.
func TestUnitEvents(t *testing.T) {
	kinds := outbox.NewKinds()
	if err := RegisterEvents(kinds); err != nil {
		t.Fatal(err)
	}
	if err := RegisterEvents(kinds); !errors.Is(err, outbox.ErrKind) {
		t.Errorf("a second registration = %v, want ErrKind", err)
	}
	doc := []byte(`{"state": "QUEUED", "username": null, "note": null,
		"run": {"id": "01M75S3Z1Q5Q3SPXB86FFN5ES8", "type": "TRACKED", "state": "QUEUED",
			"commit": {"author": "Alice", "hash": "abc", "message": "m", "url": "", "tag": "",
				"created_at": 1},
			"created_at": 1, "updated_at": 1, "drift_detection": false, "triggered_by": "alice"},
		"stack": {"id": "network", "ulid": "01M75S3Z1Q5Q3SPXB86FFN5ES8", "name": "Network"}}`)
	if err := kinds.Validate(runsapi.UpdatedKind, runsapi.UpdatedVersion, doc); err != nil {
		t.Errorf("a run_updated document fails its schema: %v", err)
	}
	if err := kinds.Validate(runsapi.UpdatedKind, runsapi.UpdatedVersion,
		[]byte(`{"state": "QUEUED"}`)); err == nil {
		t.Error("a document without its run passes")
	}
	d := New(Deps{})
	d.BindSources(sources{}, sources{})
	if jobs := d.Jobs(); len(jobs) != 1 || jobs[0].Kind.Name != "run.schedule" ||
		jobs[0].Handler == nil {
		t.Errorf("Jobs = %+v", jobs)
	}
	events := outbox.New(nil, nil, kinds)
	if err := d.Subscribe(events); err != nil {
		t.Fatal(err)
	}
	if err := d.Subscribe(events); !errors.Is(err, outbox.ErrConsumer) {
		t.Errorf("a second subscription = %v, want ErrConsumer", err)
	}
}
