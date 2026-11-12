package sample_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/sample"
)

var errInjected = errors.New("injected")

// pool is a pool the store samples, with its workers.
type pool struct {
	acct             kernel.AccountID
	id               kernel.ULID
	busy, registered int
}

type key struct {
	acct kernel.AccountID
	pool kernel.ULID
	at   time.Time
}

// store keeps samples in memory as the pg adapter does: one a pool a
// minute, the first kept. fail names the call that fails, full makes
// every prune delete a whole batch, and befores are the prunes' cut-offs.
type store struct {
	pools   []pool
	rows    map[key]workersapi.Sample
	fail    string
	full    bool
	prunes  int
	befores []time.Time
}

func (s *store) Sample(_ context.Context, at time.Time, queued map[kernel.ULID]int) (int, error) {
	if s.fail == "Sample" {
		return 0, errInjected
	}
	n := 0
	for _, p := range s.pools {
		k := key{p.acct, p.id, at}
		if _, ok := s.rows[k]; ok {
			continue
		}
		s.rows[k] = workersapi.Sample{
			At: at, Busy: p.busy, Registered: p.registered, Queued: queued[p.id],
		}
		n++
	}
	return n, nil
}

func (s *store) Prune(_ context.Context, before time.Time, batch int) (int, error) {
	if s.fail == "Prune" {
		return 0, errInjected
	}
	s.prunes++
	s.befores = append(s.befores, before)
	if s.full {
		return batch, nil
	}
	n := 0
	for k := range s.rows {
		if n < batch && k.at.Before(before) {
			delete(s.rows, k)
			n++
		}
	}
	return n, nil
}

func (s *store) Between(
	_ context.Context,
	acct kernel.AccountID,
	pool kernel.ULID,
	from, to time.Time,
) ([]workersapi.Sample, error) {
	if s.fail == "Between" {
		return nil, errInjected
	}
	var out []workersapi.Sample
	for k, v := range s.rows {
		if k.acct == acct && k.pool == pool && !k.at.Before(from) && k.at.Before(to) {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b workersapi.Sample) int { return a.At.Compare(b.At) })
	return out, nil
}

// scheduler answers queued, or err.
type scheduler struct {
	workersapi.Scheduler
	queued map[kernel.ULID]int
	err    error
}

func (s *scheduler) Queued(context.Context) (map[kernel.ULID]int, error) {
	return maps.Clone(s.queued), s.err
}

// at is s's sample at at.
func at(at time.Time, s workersapi.Sample) workersapi.Sample {
	s.At = at
	return s
}

// TestUnitWorkerPoolSample checks, in virtual time, that passes every 20
// seconds write one sample a pool a minute, at the minute, with its
// pool's busy, registered and queued counts, that SamplesBetween answers
// a window oldest first and an empty window nothing, and that a sample is
// deleted on the first pass once it is older than
// worker.samples.retention, 35 days by default.
func TestUnitWorkerPoolSample(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		acct, p, q := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
		st := &store{
			pools: []pool{{acct, p, 1, 2}, {acct, q, 0, 0}}, rows: map[key]workersapi.Sample{},
		}
		svc := sample.New(sample.Deps{Store: st})
		svc.Bind(&scheduler{queued: map[kernel.ULID]int{p: 3}})
		start := time.Now().Add(10 * time.Second)
		<-time.After(10 * time.Second)

		written := 0
		for range 9 {
			r, err := svc.Sample(ctx)
			if err != nil || r.Pruned != 0 {
				t.Fatalf("Sample = %+v, %v", r, err)
			}
			written += r.Written
			<-time.After(20 * time.Second)
		}
		if written != 6 {
			t.Errorf("three minutes of passes wrote %d samples, want 6", written)
		}
		minute := start.UTC().Truncate(time.Minute)
		busy := workersapi.Sample{Busy: 1, Registered: 2, Queued: 3}
		want := []workersapi.Sample{
			at(minute, busy), at(minute.Add(time.Minute), busy),
			at(minute.Add(2*time.Minute), busy),
		}
		got, err := svc.SamplesBetween(ctx, acct, p, minute, time.Now())
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("SamplesBetween = %+v, %v, want %+v", got, err, want)
		}
		if got, err := svc.SamplesBetween(
			ctx,
			acct,
			q,
			minute,
			minute.Add(time.Minute),
		); err != nil ||
			!slices.Equal(got, []workersapi.Sample{{At: minute}}) {
			t.Errorf("the idle pool's samples = %+v, %v", got, err)
		}
		st.fail = "Between"
		if got, err := svc.SamplesBetween(ctx, acct, p, minute, minute); err != nil || got != nil {
			t.Errorf("an empty window = %+v, %v", got, err)
		}
		st.fail = ""

		// The first sample is 35 days old at minute + 35 days; the next
		// pass after that deletes it, and only it.
		<-time.After(time.Until(minute.Add(sample.DefaultRetention)))
		r, err := svc.Sample(ctx)
		if err != nil || r.Pruned != 0 || r.Written != 2 {
			t.Errorf("the pass at 35 days = %+v, %v", r, err)
		}
		<-time.After(time.Second)
		if r, err := svc.Sample(ctx); err != nil || r.Pruned != 2 || r.Written != 0 {
			t.Errorf("the pass past 35 days = %+v, %v", r, err)
		}
		got, _ = svc.SamplesBetween(ctx, acct, p, minute, time.Now())
		if len(got) != 3 || !got[0].At.Equal(minute.Add(time.Minute)) {
			t.Errorf("after the prune = %+v", got)
		}
	})
}

// TestUnitWorkerPoolSampleRetention checks that a set retention is the
// prune's cut-off, and that a pass stops after PruneRounds full batches.
func TestUnitWorkerPoolSampleRetention(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 30, 45, 0, time.UTC)
	st := &store{rows: map[key]workersapi.Sample{}, full: true}
	svc := sample.New(sample.Deps{
		Store: st, Retention: time.Hour, Now: func() time.Time { return now },
	})
	r, err := svc.Sample(t.Context())
	if err != nil || r.Pruned != sample.PruneBatch*sample.PruneRounds ||
		st.prunes != sample.PruneRounds {
		t.Errorf("Sample = %+v, %v after %d prunes", r, err, st.prunes)
	}
	if !st.befores[0].Equal(now.Add(-time.Hour)) {
		t.Errorf("pruned before %s, want an hour before %s", st.befores[0], now)
	}
}

// TestUnitWorkerPoolSampleUnbound checks that, until the scheduler is
// bound, a pass samples each pool with no queued run.
func TestUnitWorkerPoolSampleUnbound(t *testing.T) {
	acct, p := kernel.NewAccountID(), kernel.NewULID()
	st := &store{pools: []pool{{acct, p, 1, 1}}, rows: map[key]workersapi.Sample{}}
	svc := sample.New(sample.Deps{Store: st})
	if r, err := svc.Sample(t.Context()); err != nil || r.Written != 1 {
		t.Fatalf("Sample = %+v, %v", r, err)
	}
	for k, v := range st.rows {
		if k.pool != p || v.Queued != 0 || v.Busy != 1 {
			t.Errorf("sample %+v = %+v", k, v)
		}
	}
}

// TestUnitWorkerPoolSampleFailures checks that a failing count, write,
// prune or read reaches the caller, and a failing count writes nothing.
func TestUnitWorkerPoolSampleFailures(t *testing.T) {
	acct, p := kernel.NewAccountID(), kernel.NewULID()
	st := &store{pools: []pool{{acct, p, 0, 1}}, rows: map[key]workersapi.Sample{}}
	svc := sample.New(sample.Deps{Store: st})
	sch := &scheduler{err: errInjected}
	svc.Bind(sch)
	if _, err := svc.Sample(t.Context()); !errors.Is(err, errInjected) || len(st.rows) != 0 {
		t.Errorf("a failing count = %v, wrote %d", err, len(st.rows))
	}
	sch.err = nil
	for _, fail := range []string{"Sample", "Prune"} {
		st.fail = fail
		if _, err := svc.Sample(t.Context()); !errors.Is(err, errInjected) {
			t.Errorf("%s failing = %v", fail, err)
		}
	}
	st.fail = "Between"
	if _, err := svc.SamplesBetween(t.Context(), acct, p, time.Time{}, time.Now()); !errors.Is(
		err, errInjected) {
		t.Errorf("Between failing = %v", err)
	}
}
