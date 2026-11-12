package deps

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// TestUnitEventually runs in virtual time, so its deadlines cost nothing.
func TestUnitEventually(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		polls := 0
		err := Eventually(t.Context(), "the third poll", func(context.Context) (bool, error) {
			polls++
			return polls == 3, nil
		})
		if err != nil || polls != 3 {
			t.Errorf("Eventually = %v after %d polls", err, polls)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		last := errors.New("connection refused")
		err := Eventually(t.Context(), "a dependency", func(context.Context) (bool, error) {
			return false, last
		})
		if !errors.Is(err, ErrTimedOut) || !errors.Is(err, last) ||
			err.Error() != "waiting for a dependency: timed out (last: connection refused)" {
			t.Errorf("Eventually = %v", err)
		}
		if elapsed := time.Since(start); elapsed < WaitLimit {
			t.Errorf("gave up after %s, before the %s limit", elapsed, WaitLimit)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		err := Eventually(ctx, "nothing", func(context.Context) (bool, error) { return false, nil })
		if err == nil || err.Error() != "waiting for nothing: timed out" {
			t.Errorf("Eventually = %v", err)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		polls := 0
		err := Eventually(t.Context(), "a process", func(context.Context) (bool, error) {
			polls++
			return false, ErrPermanent
		})
		if !errors.Is(err, ErrPermanent) || polls != 1 {
			t.Errorf("a permanent error polled %d times: %v", polls, err)
		}
	})
}
