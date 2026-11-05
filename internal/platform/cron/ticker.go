package cron

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Batch is how many due rows one transaction fires per source, decision
// 0011's 100.
const Batch = 100

// Source is a table whose rows fire on a schedule, such as periodic_tasks.
type Source interface {
	// Name labels the source in logs.
	Name() string
	// Fire runs inside the ticker's transaction on ctx. It locks up to
	// limit due rows with FOR UPDATE SKIP LOCKED, so another replica's
	// ticker passes over them; enqueues a fire job for each with EnqueueTx,
	// policy Ignore and a unique key naming the row and its fire time;
	// stores each row's next fire time, the first one in the future; and
	// returns how many rows fired.
	Fire(ctx context.Context, limit int) (int, error)
}

// Ticker fires the due rows of its sources. Every replica in the scheduler
// mode runs one, and their locks and the jobs' unique keys fire each due
// time once, with no leader.
type Ticker struct {
	db      tx.Beginner
	every   time.Duration
	sources []Source
	log     *slog.Logger
}

// NewTicker fires sources' due rows in db every interval.
func NewTicker(db tx.Beginner, every time.Duration, log *slog.Logger, sources ...Source) *Ticker {
	return &Ticker{db: db, every: every, sources: sources, log: log}
}

// Tick fires every source's due rows once: a transaction per batch, and
// another while a batch comes back full. A failing source does not stop the
// others; Tick returns their errors joined.
func (t *Ticker) Tick(ctx context.Context) error {
	var errs []error
	for _, s := range t.sources {
		for {
			var n int
			err := tx.Do(ctx, t.db, tx.Options{}, func(ctx context.Context) error {
				var err error
				if n, err = s.Fire(ctx, Batch); err != nil {
					return fmt.Errorf("fire %s: %w", s.Name(), err)
				}
				return nil
			})
			if err != nil {
				errs = append(errs, err)
				break
			}
			if n > 0 {
				t.log.Debug("schedules fired", "source", s.Name(), "rows", n)
			}
			if n < Batch {
				break
			}
		}
	}
	return errors.Join(errs...)
}

// Run ticks at once and then every interval until ctx ends. A failing tick
// is logged at warn once, and again only after a tick has succeeded.
func (t *Ticker) Run(ctx context.Context) {
	t.log.Info("scheduler ticker started", "tick", t.every.String(), "sources", len(t.sources))
	every := time.NewTicker(t.every)
	defer every.Stop()
	failing := false
	for {
		err := t.Tick(ctx)
		switch {
		case err != nil && ctx.Err() == nil && !failing:
			failing = true
			t.log.Warn("scheduler tick failed", "error", err)
		case err == nil && failing:
			failing = false
			t.log.Info("scheduler tick recovered")
		}
		select {
		case <-ctx.Done():
			t.log.Info("scheduler ticker stopped")
			return
		case <-every.C:
		}
	}
}
