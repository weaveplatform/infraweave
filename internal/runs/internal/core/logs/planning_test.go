package logs_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/logs"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
)

func (s *store) PlanningState(_ context.Context, account kernel.AccountID, run kernel.ULID, version int64) (logs.PlanningState, error) {
	if err := s.err("PlanningState"); err != nil {
		return logs.PlanningState{}, err
	}
	if s.run.Account != account || s.run.ID != run || s.states[version] != kernel.RunStatePlanning {
		return logs.PlanningState{}, runsapi.ErrPlanningLogNotFound
	}
	out := logs.PlanningState{Deleted: s.run.LogsDeletedAt != nil, Chunks: s.counts[version]}
	for _, chunk := range s.chunks[version] {
		out.Chunks = max(out.Chunks, chunk.Seq+1)
	}
	return out, nil
}

func planningFixture(bodies ...string) (*store, *objects) {
	s := &store{
		run:    lifecycle.Run{Account: kernel.NewAccountID(), ID: kernel.NewULID(), State: kernel.RunStateFinished, Version: 8},
		states: map[int64]kernel.RunState{3: kernel.RunStatePlanning, 4: kernel.RunStateUnconfirmed, 7: kernel.RunStatePlanning, 8: kernel.RunStateFinished},
		chunks: map[int64][]report.Chunk{}, counts: map[int64]int64{3: int64(len(bodies))},
	}
	o := newObjects()
	for n, body := range bodies {
		key := fmt.Sprintf("chunk/%d", n)
		o.put(key, body)
		s.chunks[3] = append(s.chunks[3], report.Chunk{Version: 3, State: kernel.RunStatePlanning, Seq: int64(n), Key: key, Bytes: int64(len(body))})
	}
	return s, o
}

func planningRead(t *testing.T, s *store, o logs.PlanningObjects, budget int) runsapi.PlanningLog {
	t.Helper()
	out, err := logs.NewPlanningReader(s, o).PlanningLog(t.Context(), s.run.Account, s.run.ID, 3, budget)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUnitPlanningLogTail(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		bodies []string
		budget int
		want   runsapi.PlanningLog
	}{
		{"masked exact bytes", []string{"\x1b[32msecret=***MASKED***\x1b[0m\n", "\nlast\n"}, 100, runsapi.PlanningLog{Text: "\x1b[32msecret=***MASKED***\x1b[0m\n\nlast\n"}},
		{"chunk boundaries add no newline", []string{"hello", " world\nnext"}, 100, runsapi.PlanningLog{Text: "hello world\nnext"}},
		{"whole lines", []string{"one\ntwo\nthree\n"}, 10, runsapi.PlanningLog{Text: "two\nthree\n", CutLines: 1}},
		{"partial first line", []string{"prefix\n" + strings.Repeat("x", 200) + "suffix"}, 6, runsapi.PlanningLog{Text: "suffix", CutLines: 1, PartialLine: true}},
		{"split utf8", []string{"α\n😀tail\n"}, 7, runsapi.PlanningLog{Text: "tail\n", CutLines: 1, PartialLine: true}},
		{"empty", nil, 32, runsapi.PlanningLog{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, o := planningFixture(tc.bodies...)
			if got := planningRead(t, s, o, tc.budget); got != tc.want {
				t.Fatalf("got=%+v want=%+v", got, tc.want)
			}
		})
	}
}

type planningPages struct {
	*store
	from   []int64
	limits []int
}

func (s *planningPages) Chunks(ctx context.Context, account kernel.AccountID, run kernel.ULID, version, from int64, limit int) ([]report.Chunk, error) {
	s.from = append(s.from, from)
	s.limits = append(s.limits, limit)
	return s.store.Chunks(ctx, account, run, version, from, limit)
}

func TestUnitPlanningLogPagesAndGaps(t *testing.T) {
	t.Parallel()
	bodies := make([]string, 130)
	for n := range bodies {
		bodies[n] = fmt.Sprintf("chunk%03d\n", n)
	}
	s, o := planningFixture(bodies...)
	paged := &planningPages{store: s}
	got, err := logs.NewPlanningReader(paged, o).PlanningLog(t.Context(), s.run.Account, s.run.ID, 3, 18)
	if err != nil || got != (runsapi.PlanningLog{Text: "chunk128\nchunk129\n", CutLines: 128}) || !slices.Equal(paged.from, []int64{0, 64, 128}) ||
		!slices.Equal(paged.limits, []int{64, 64, 64}) {
		t.Fatalf("paged tail=%+v error=%v pages=%v limits=%v", got, err, paged.from, paged.limits)
	}
	s, o = planningFixture("kept\n", "expired\n", "last\n")
	s.chunks[3][0].Lost = true
	s.chunks[3][0].Key = ""
	delete(o.rows, "chunk/1")
	s.chunks[3] = append(s.chunks[3][:2], report.Chunk{Version: 3, State: kernel.RunStatePlanning, Seq: 3, Key: "chunk/2"})
	s.counts[3] = 4
	got = planningRead(t, s, o, 1024)
	if got != (runsapi.PlanningLog{Text: strings.Repeat(logs.Missing+"\n", 3) + "last\n", Unavailable: true}) {
		t.Fatalf("lost/expired/unindexed chunks erase useful text: %+v", got)
	}
	s, o = planningFixture()
	s.counts[3] = 1_000_000_000
	got = planningRead(t, s, o, len(logs.Missing)+1)
	if got != (runsapi.PlanningLog{Text: logs.Missing + "\n", CutLines: 999_999_999, Unavailable: true}) {
		t.Fatalf("huge sparse extent: %+v", got)
	}
}

func TestUnitPlanningLogExactScopeAndAvailability(t *testing.T) {
	t.Parallel()
	s, o := planningFixture("first planning\n")
	s.chunks[7] = []report.Chunk{{Version: 7, State: kernel.RunStatePlanning, Key: "second", Seq: 0}}
	o.put("second", "second planning\n")
	r := logs.NewPlanningReader(s, o)
	for _, scope := range []struct {
		account kernel.AccountID
		id      kernel.ULID
		version int64
	}{
		{kernel.NewAccountID(), s.run.ID, 3}, {s.run.Account, kernel.NewULID(), 3}, {s.run.Account, s.run.ID, 4}, {s.run.Account, s.run.ID, 6}, {kernel.AccountID{}, s.run.ID, 3}, {s.run.Account, kernel.ULID{}, 3},
	} {
		if _, err := r.PlanningLog(t.Context(), scope.account, scope.id, scope.version, 100); !errors.Is(err, runsapi.ErrPlanningLogNotFound) {
			t.Fatal("wrong account/run/state version exposed", err)
		}
	}
	if got := planningRead(t, s, o, 100); got.Text != "first planning\n" {
		t.Fatal("latest planning replaced exact version", got)
	}
	if got, err := r.PlanningLog(t.Context(), s.run.Account, s.run.ID, 7, 100); err != nil || got.Text != "second planning\n" {
		t.Fatal(got, err)
	}
	s.run.LogsDeletedAt = new(s.entered)
	if got := planningRead(t, s, o, 100); got != (runsapi.PlanningLog{Unavailable: true}) {
		t.Fatal("deleted log remained readable", got)
	}
	s.run.LogsDeletedAt = nil
	delete(o.rows, "chunk/0")
	if got := planningRead(t, s, o, 100); !got.Unavailable || got.Text != logs.Missing+"\n" {
		t.Fatal("expired log appeared available", got)
	}
}

type planningOpen struct {
	open func(context.Context) (io.ReadCloser, error)
}

func (o planningOpen) Open(ctx context.Context, _ objectstore.Name, _ string) (io.ReadCloser, error) {
	return o.open(ctx)
}

type planningReadCloser struct {
	io.Reader
	close func() error
}

func (r planningReadCloser) Close() error { return r.close() }

type failingPlanningReader struct{}

func (failingPlanningReader) Read([]byte) (int, error) { return 0, errDown }

type repeatingPlanningReader struct{ remaining int }

func (r *repeatingPlanningReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	for i := range n {
		p[i] = 'x'
	}
	r.remaining -= n
	return n, nil
}

func TestUnitPlanningLogHugeSingleLine(t *testing.T) {
	t.Parallel()
	s, _ := planningFixture("placeholder")
	closed := 0
	o := planningOpen{open: func(context.Context) (io.ReadCloser, error) {
		return planningReadCloser{Reader: &repeatingPlanningReader{remaining: 128 << 20}, close: func() error { closed++; return nil }}, nil
	}}
	got := planningRead(t, s, o, 24)
	if got != (runsapi.PlanningLog{Text: strings.Repeat("x", 24), PartialLine: true}) || closed != 1 {
		t.Fatal("huge unbroken line tail/close", got, closed)
	}
}

func TestUnitPlanningLogFailures(t *testing.T) {
	t.Parallel()
	s, o := planningFixture("body\n")
	r := logs.NewPlanningReader(s, o)
	for _, input := range []struct {
		version int64
		budget  int
	}{{0, 1}, {-1, 1}, {3, 0}, {3, runsapi.MaxPlanningLogBytes + 1}} {
		if _, err := r.PlanningLog(t.Context(), s.run.Account, s.run.ID, input.version, input.budget); !errors.Is(err, runsapi.ErrPlanningLogInput) {
			t.Fatal("invalid read accepted", err)
		}
	}
	for _, failure := range []string{"PlanningState", "Chunks"} {
		s.fail = failure
		if _, err := r.PlanningLog(t.Context(), s.run.Account, s.run.ID, 3, 100); !errors.Is(err, errDown) {
			t.Fatal("store failure hidden", failure, err)
		}
	}
	s.fail = ""
	o.fail = "Open"
	if _, err := r.PlanningLog(t.Context(), s.run.Account, s.run.ID, 3, 100); !errors.Is(err, errDown) {
		t.Fatal("object failure became absent log", err)
	}
	for _, readFault := range []bool{false, true} {
		closed := 0
		objects := planningOpen{open: func(context.Context) (io.ReadCloser, error) {
			var reader io.Reader = strings.NewReader("ok")
			if readFault {
				reader = failingPlanningReader{}
			}
			return planningReadCloser{Reader: reader, close: func() error {
				closed++
				if !readFault {
					return errDown
				}
				return nil
			}}, nil
		}}
		if _, err := logs.NewPlanningReader(s, objects).PlanningLog(t.Context(), s.run.Account, s.run.ID, 3, 100); !errors.Is(err, errDown) || closed != 1 {
			t.Fatal("read/close failure lost", err, closed)
		}
	}
	o.fail = ""
	s.chunks[3][0].State = kernel.RunStateApplying
	if _, err := r.PlanningLog(t.Context(), s.run.Account, s.run.ID, 3, 100); err == nil {
		t.Fatal("wrong-state chunk accepted")
	}
	s.chunks[3][0].State = kernel.RunStatePlanning
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.PlanningLog(ctx, s.run.Account, s.run.ID, 3, 100); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation hidden", err)
	}
}

type cancelPlanningReader struct{ cancel context.CancelFunc }

func (r cancelPlanningReader) Read(p []byte) (int, error) {
	copy(p, "first")
	r.cancel()
	return 5, nil
}

func TestUnitPlanningLogCanceledStream(t *testing.T) {
	t.Parallel()
	s, _ := planningFixture("body")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	closed := 0
	objects := planningOpen{open: func(context.Context) (io.ReadCloser, error) {
		return planningReadCloser{Reader: cancelPlanningReader{cancel: cancel}, close: func() error { closed++; return nil }}, nil
	}}
	if _, err := logs.NewPlanningReader(s, objects).PlanningLog(ctx, s.run.Account, s.run.ID, 3, 20); !errors.Is(err, context.Canceled) || closed != 1 {
		t.Fatal("canceled stream did not close", err, closed)
	}
}
