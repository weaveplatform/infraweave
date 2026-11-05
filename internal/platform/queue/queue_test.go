package queue

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitBackoff(t *testing.T) {
	for _, tc := range []struct {
		attempt int
		ceiling time.Duration
	}{
		{0, time.Second},
		{1, time.Second},
		{2, 2 * time.Second},
		{5, 16 * time.Second},
		{10, 512 * time.Second},
		{11, 15 * time.Minute},
		{40, 15 * time.Minute},
	} {
		lowest, highest := time.Duration(1<<62), time.Duration(0)
		for range 2000 {
			d := Backoff(tc.attempt)
			lowest, highest = min(lowest, d), max(highest, d)
		}
		if lowest < time.Second || highest > tc.ceiling {
			t.Errorf("attempt %d: delays in [%v, %v], want within [1s, %v]",
				tc.attempt, lowest, highest, tc.ceiling)
		}
		// Full jitter spreads over the whole range, so 2000 draws come
		// near both ends of any range wider than a second.
		if tc.ceiling > time.Second && highest-lowest < (tc.ceiling-time.Second)/2 {
			t.Errorf("attempt %d: delays only span [%v, %v]", tc.attempt, lowest, highest)
		}
	}
}

func TestUnitLastError(t *testing.T) {
	if got := lastError(nil); got != "unknown error" {
		t.Errorf("nil: %q", got)
	}
	// "é" is two bytes, so the cut at MaxLastError falls inside one.
	long := errors.New("x" + strings.Repeat("é", MaxLastError))
	got := lastError(long)
	if len(got) > MaxLastError || !utf8.ValidString(got) || len(got) < MaxLastError-1 {
		t.Errorf("truncated to %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	if got := lastError(errors.New("short")); got != "short" {
		t.Errorf("short: %q", got)
	}
}

func TestUnitEnqueueRefusesBeforeTheDatabase(t *testing.T) {
	q := &Queue{}
	ok := Kind{Name: "test.ok", Queue: Default, Policy: Ignore}
	for name, tc := range map[string]struct {
		kind    Kind
		payload any
		opts    []Option
		want    error
	}{
		"no name":         {Kind{Queue: Default, Policy: Ignore}, nil, nil, ErrKind},
		"unknown queue":   {Kind{Name: "a.b", Queue: "nope", Policy: Ignore}, nil, nil, ErrKind},
		"no policy":       {Kind{Name: "a.b", Queue: Default}, nil, nil, ErrKind},
		"negative limit":  {Kind{Name: "a.b", Queue: Default, Policy: Coalesce, MaxAttempts: -1}, nil, nil, ErrKind},
		"priority high":   {ok, nil, []Option{Priority(10)}, ErrJob},
		"priority low":    {ok, nil, []Option{Priority(-1)}, ErrJob},
		"no attempts":     {ok, nil, []Option{MaxAttempts(0)}, ErrJob},
		"not JSON":        {ok, func() {}, nil, ErrJob},
		"too large":       {ok, strings.Repeat("a", MaxPayload), nil, ErrPayloadTooLarge},
		"no transaction":  {ok, map[string]string{"id": "1"}, nil, ErrNoTransaction},
		"largest allowed": {ok, strings.Repeat("a", MaxPayload-2), nil, ErrNoTransaction},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := q.EnqueueTx(
				t.Context(),
				tc.kind,
				tc.payload,
				tc.opts...); !errors.Is(
				err,
				tc.want,
			) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if err := q.CompleteTx(t.Context(), Job{}); !errors.Is(err, ErrNoTransaction) {
		t.Errorf("CompleteTx outside a transaction: %v", err)
	}
	if got := (Kind{MaxAttempts: 3}).maxAttempts(); got != 3 {
		t.Errorf("an explicit limit became %d", got)
	}
}

// TestUnitUnreachable checks that every operation on a server that refuses
// connections reports kernel.ErrUnavailable, so a request that hits it
// answers 503 and a worker backs off rather than killing jobs.
func TestUnitUnreachable(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://iw:iw@127.0.0.1:1/iw?connect_timeout=5")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	q := New(pool)
	ctx := t.Context()
	k := Kind{Name: "test.work", Queue: Default, Policy: Ignore}
	j := Job{ID: 1, Generation: 1}
	for name, f := range map[string]func() error{
		"Enqueue":  func() error { _, err := q.Enqueue(ctx, k, nil); return err },
		"Claim":    func() error { _, err := q.Claim(ctx, Default, []string{k.Name}, 1, time.Minute, "t"); return err },
		"Complete": func() error { return q.Complete(ctx, j) },
		"Recover":  func() error { _, err := q.Recover(ctx); return err },
		"Prune":    func() error { _, err := q.Prune(ctx, time.Hour, time.Hour); return err },
		"ListDead": func() error { _, err := q.ListDead(ctx, 0, 10); return err },
		"Retry":    func() error { return q.Retry(ctx, 1) },
		"Discard":  func() error { return q.Discard(ctx, 1) },
		"Stats":    func() error { _, err := q.Stats(ctx); return err },
	} {
		if err := f(); !errors.Is(err, kernel.ErrUnavailable) {
			t.Errorf("%s: %v, want kernel.ErrUnavailable", name, err)
		}
	}
}
