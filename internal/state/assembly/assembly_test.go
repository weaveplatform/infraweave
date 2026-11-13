package assembly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/state/internal/pg"
)

// TestUnitDomain checks that the domain constructs its core and backend,
// declares its stream and held columns, its stack guard, its purge job
// and its versions, subscribes its lock release once, and that its
// routes need a run on a domain and answer 501 on none.
func TestUnitDomain(t *testing.T) {
	d := New(Deps{})
	if d == nil || d.state == nil || d.backend == nil || d.Versions() == nil {
		t.Fatal("the domain has no core, backend or versions")
	}
	if Stream().Name != pg.StreamName || !reflect.DeepEqual(Held(), pg.Held()) {
		t.Errorf("stream %q, held %v", Stream().Name, Held())
	}
	if jobs := d.Jobs(); len(jobs) != 1 || jobs[0].Kind.Name != "state.purge" ||
		jobs[0].Handler == nil || d.StackGuard() == nil {
		t.Errorf("Jobs = %+v, guard %v", jobs, d.StackGuard())
	}
	events := outbox.New(nil, nil, outbox.NewKinds())
	if err := d.Subscribe(events); err != nil {
		t.Fatal(err)
	}
	if err := d.Subscribe(events); !errors.Is(err, outbox.ErrConsumer) {
		t.Errorf("a second subscription = %v, want ErrConsumer", err)
	}
	for _, tc := range []struct {
		d      *Domain
		status int
	}{{d, http.StatusUnauthorized}, {nil, http.StatusNotImplemented}} {
		r := chi.NewRouter()
		tc.d.Routes(r)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/state/v1/stacks/one", nil))
		if rec.Code != tc.status {
			t.Errorf("a domain %v answered %d, want %d", tc.d != nil, rec.Code, tc.status)
		}
	}
}

// TestUnitSettings checks that a state version is bounded at 512 MiB by
// default, and that a bound that is not positive is refused.
func TestUnitSettings(t *testing.T) {
	if d := DefaultSettings(); d.MaxSize != 512<<20 || d.Validate() != nil {
		t.Errorf("defaults %+v: %v", d, d.Validate())
	}
	for _, size := range []int64{0, -1} {
		if err := (Settings{MaxSize: size}).Validate(); !errors.Is(err, ErrConfig) ||
			!strings.Contains(err.Error(), "state.max_size") {
			t.Errorf("max_size %d: %v", size, err)
		}
	}
}

// TestUnitRunUpdated checks that a run_updated reaches the lock release
// with its account, run and state, and that a document that does not
// decode fails its job for good.
func TestUnitRunUpdated(t *testing.T) {
	type ended struct {
		acct  kernel.AccountID
		run   kernel.ULID
		state kernel.RunState
	}
	var got []ended
	h := runUpdated(func(
		_ context.Context,
		acct kernel.AccountID,
		run kernel.ULID,
		st kernel.RunState,
	) error {
		got = append(got, ended{acct, run, st})
		return nil
	})
	acct, run := kernel.NewULID(), kernel.NewULID()
	doc, err := json.Marshal(runsapi.Updated{
		State: kernel.RunStateFailed, Run: runsapi.UpdatedRun{ID: run},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h(t.Context(), outbox.Event{Account: acct, Document: doc}, queue.Job{}); err != nil ||
		!reflect.DeepEqual(
			got,
			[]ended{{kernel.AccountID{ULID: acct}, run, kernel.RunStateFailed}},
		) {
		t.Errorf("a run_updated = %v, ended %+v", err, got)
	}
	err = h(
		t.Context(),
		outbox.Event{Kind: runsapi.UpdatedKind, Document: []byte("{")},
		queue.Job{},
	)
	if fmt.Sprintf("%T", err) != fmt.Sprintf("%T", queue.Permanent(err)) || len(got) != 1 {
		t.Errorf("a document that does not decode = %T %v", err, err)
	}
}
