package deps

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Polling starts fast, so a ready dependency costs little, and backs off so
// a slow one is not hammered.
const (
	firstPoll = 10 * time.Millisecond
	maxPoll   = 500 * time.Millisecond
	// WaitLimit bounds a wait whose context has no deadline: long enough
	// for a container image's first start, short enough that a hung
	// dependency fails the scenario rather than the CI job.
	WaitLimit = 2 * time.Minute
)

// ErrTimedOut is returned when a condition is still false at the deadline.
var ErrTimedOut = errors.New("timed out")

// Eventually polls cond until it reports true, returns an error, or ctx's
// deadline (or WaitLimit) passes. It is the suite's only way to wait: no
// step sleeps for a fixed delay. The error names what it waited for and
// cond's last error, so a timed-out scenario says why.
//
// ref: decision 0016
func Eventually(ctx context.Context, what string, cond func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, WaitLimit)
	defer cancel()
	wait := firstPoll
	var last error
	for {
		ok, err := cond(ctx)
		switch {
		case ok:
			return nil
		case errors.Is(err, ErrPermanent):
			return fmt.Errorf("waiting for %s: %w", what, err)
		}
		last = err
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			if last != nil {
				return fmt.Errorf("waiting for %s: %w (last: %w)", what, ErrTimedOut, last)
			}
			return fmt.Errorf("waiting for %s: %w", what, ErrTimedOut)
		case <-t.C:
		}
		wait = min(2*wait, maxPoll)
	}
}

// ErrPermanent marks a condition error that no later poll can change, such
// as a process that has exited; Eventually returns it at once.
var ErrPermanent = errors.New("will not change")
