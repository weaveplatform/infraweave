package delivery_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/events/internal/core/delivery"
	"github.com/weaveplatform/infraweave/internal/events/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

var errStop = errors.New("stop")

type feedback struct {
	vcsapi.Feedback
	targets          []vcsapi.FeedbackTarget
	order            *[]string
	compute, enqueue error
}

func (f feedback) Targets(context.Context, outbox.Event) ([]vcsapi.FeedbackTarget, error) {
	*f.order = append(*f.order, "targets")
	return f.targets, f.compute
}

func (f feedback) Enqueue(_ context.Context, target vcsapi.FeedbackTarget) error {
	*f.order = append(*f.order, "enqueue:"+target.Key)
	return f.enqueue
}

type store struct {
	port.Store
	order          *[]string
	inserted       map[string]bool
	fail           error
	pruned, purged []int64
}

func (s *store) Insert(_ context.Context, d port.Delivery) (bool, error) {
	*s.order = append(*s.order, "insert:"+d.Target)
	if d.Sink != delivery.CommitChecks || d.RuleIndex != 0 || d.CreatedAt.IsZero() {
		return false, fmt.Errorf("invalid delivery: %+v", d)
	}
	return s.inserted[d.Target], s.fail
}

func (s *store) Prune(context.Context, time.Time, int) (int64, error) {
	n := s.pruned[0]
	s.pruned = s.pruned[1:]
	return n, s.fail
}

func (s *store) Purge(context.Context, kernel.ULID, int) (int64, error) {
	n := s.purged[0]
	s.purged = s.purged[1:]
	return n, s.fail
}

type outcomes map[string]int64

func (o outcomes) Add(_ context.Context, outcome string, n int64) { o[outcome] += n }

func TestUnitSinksConsume(t *testing.T) {
	event := outbox.Event{Account: kernel.NewULID(), ID: kernel.NewULID()}
	target := func(key string) vcsapi.FeedbackTarget {
		return vcsapi.FeedbackTarget{Account: event.Account, EventID: event.ID, Key: key, Job: "vcs.report_check", Payload: []byte(`{}`)}
	}
	for _, tt := range []struct {
		name                               string
		targets                            []vcsapi.FeedbackTarget
		inserted                           map[string]bool
		compute, insert, enqueue, complete error
		want                               []string
		metrics                            outcomes
	}{
		{
			"two targets",
			[]vcsapi.FeedbackTarget{target("a"), target("b")},
			map[string]bool{"a": true, "b": true},
			nil, nil, nil, nil,
			[]string{"targets", "atomic", "insert:a", "enqueue:a", "insert:b", "enqueue:b", "complete"},
			outcomes{"delivered": 2},
		},
		{
			"duplicate targets",
			[]vcsapi.FeedbackTarget{target("a"), target("b")},
			map[string]bool{},
			nil, nil, nil, nil,
			[]string{"targets", "atomic", "insert:a", "insert:b", "complete"},
			outcomes{},
		},
		{"no targets", nil, nil, nil, nil, nil, nil, []string{"targets", "atomic", "complete"}, outcomes{"skipped": 1}},
		{"computation fails", nil, nil, errStop, nil, nil, nil, []string{"targets"}, outcomes{}},
		{"insert fails", []vcsapi.FeedbackTarget{target("a")}, nil, nil, errStop, nil, nil, []string{"targets", "atomic", "insert:a"}, outcomes{}},
		{"enqueue fails", []vcsapi.FeedbackTarget{target("a")}, map[string]bool{"a": true}, nil, nil, errStop, nil, []string{"targets", "atomic", "insert:a", "enqueue:a"}, outcomes{}},
		{"completion fenced", []vcsapi.FeedbackTarget{target("a")}, map[string]bool{"a": true}, nil, nil, nil, queue.ErrLeaseLost, []string{"targets", "atomic", "insert:a", "enqueue:a", "complete"}, outcomes{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var order []string
			observed := outcomes{}
			s := delivery.New(delivery.Deps{
				Store:    &store{order: &order, inserted: tt.inserted, fail: tt.insert},
				Feedback: feedback{targets: tt.targets, order: &order, compute: tt.compute, enqueue: tt.enqueue},
				Atomic: func(ctx context.Context, run func(context.Context) error) error {
					order = append(order, "atomic")
					return run(ctx)
				},
				Complete: func(context.Context, queue.Job) error { order = append(order, "complete"); return tt.complete },
				Metrics:  observed,
			})
			err := s.Consume(t.Context(), event, queue.Job{})
			wantError := tt.compute != nil || tt.insert != nil || tt.enqueue != nil || tt.complete != nil
			if (err != nil) != wantError || !reflect.DeepEqual(order, tt.want) || !reflect.DeepEqual(observed, tt.metrics) {
				t.Fatalf("consume = %v, order %v, metrics %v; want error %t, %v, %v", err, order, observed, wantError, tt.want, tt.metrics)
			}
		})
	}
}

func TestUnitSinksRejectIdentity(t *testing.T) {
	event := outbox.Event{Account: kernel.NewULID(), ID: kernel.NewULID()}
	for name, target := range map[string]vcsapi.FeedbackTarget{
		"foreign account": {Account: kernel.NewULID(), EventID: event.ID, Key: "a", Job: "vcs.report_check"},
		"another event":   {Account: event.Account, EventID: kernel.NewULID(), Key: "a", Job: "vcs.report_check"},
		"no key":          {Account: event.Account, EventID: event.ID, Job: "vcs.report_check"},
		"no job":          {Account: event.Account, EventID: event.ID, Key: "a"},
	} {
		t.Run(name, func(t *testing.T) {
			var order []string
			s := delivery.New(delivery.Deps{Feedback: feedback{targets: []vcsapi.FeedbackTarget{target}, order: &order}})
			if err := s.Consume(t.Context(), event, queue.Job{}); !errors.Is(err, vcsapi.ErrFeedbackTarget) || !reflect.DeepEqual(order, []string{"targets"}) {
				t.Fatalf("consume = %v, %v", err, order)
			}
		})
	}
}

func TestUnitSinksCleanupBatches(t *testing.T) {
	for _, failed := range []bool{false, true} {
		var cause error
		if failed {
			cause = errStop
		}
		st := &store{pruned: []int64{delivery.Batch, 1}, purged: []int64{delivery.Batch, 0}, fail: cause}
		s := delivery.New(delivery.Deps{Store: st})
		if err := s.Prune(t.Context(), time.Hour); !errors.Is(err, cause) {
			t.Fatal(err)
		}
		if err := s.Purge(t.Context(), kernel.NewULID()); !errors.Is(err, cause) {
			t.Fatal(err)
		}
		if !failed && (len(st.pruned) != 0 || len(st.purged) != 0) {
			t.Fatal("cleanup stopped before the bounded batches finished")
		}
	}
}
