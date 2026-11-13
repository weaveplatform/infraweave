package exec

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// flaky refuses its first reports with errs, then answers step; with
// always set it refuses every report with it.
type flaky struct {
	errs   []error
	always error
	sent   int
}

var errLost = errors.New("the answer was lost")

func (f *flaky) Report(context.Context, string, string, wire.RunReport) (wire.NextStep, error) {
	f.sent++
	if f.always != nil {
		return wire.NextStep{}, f.always
	}
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return wire.NextStep{}, err
	}
	return wire.NextStep{Action: wire.Exit, State: "FINISHED", StateVersion: 9}, nil
}

func reporting(f *flaky) *run {
	return &run{
		h: handoff.Handoff{Run: "run-1", RunToken: "token"},
		d: Deps{Reports: f, Log: slog.New(slog.DiscardHandler)},
	}
}

// TestUnitReportRetries checks that a lost answer, a 503 and a 429 are
// sent again after 1, 2 and 4 seconds; that a report still refused when
// the next wait would pass ten minutes answers its error; and that a
// cancelled context ends the wait.
func TestUnitReportRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &flaky{errs: []error{
			errLost, &client.Error{Status: 503}, &client.Error{Status: 429},
		}}
		began := time.Now()
		step, err := reporting(f).report(t.Context(), wire.RunReport{})
		if err != nil || step.State != "FINISHED" || f.sent != 4 ||
			time.Since(began) != 7*time.Second {
			t.Errorf("after 3 refusals: %+v, %v, %d sent after %s", step, err, f.sent,
				time.Since(began))
		}

		f = &flaky{always: &client.Error{Status: 502}}
		began = time.Now()
		if _, err := reporting(
			f,
		).report(t.Context(), wire.RunReport{}); client.Status(err) != 502 ||
			time.Since(began) > reportRetry ||
			time.Since(began) < reportRetry-reportWaitMax {
			t.Errorf("always refused: %v after %s", err, time.Since(began))
		}

		ctx, cancel := context.WithCancel(t.Context())
		f = &flaky{always: errLost}
		go func() {
			<-time.After(90 * time.Second)
			cancel()
		}()
		if _, err := reporting(f).report(ctx, wire.RunReport{}); !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled: %v", err)
		}
	})
}

func TestUnitLookupAndQuote(t *testing.T) {
	env := []string{"A=1", "B=2", "A=3", "C"}
	if lookup(env, "A") != "3" || lookup(env, "B") != "2" || lookup(env, "C") != "" ||
		lookup(env, "D") != "" {
		t.Error("lookup")
	}
	if got := line([]string{"echo", "it's", "a b"}, "/tmp/o ut"); got !=
		`'echo' 'it'\''s' 'a b' >'/tmp/o ut'` {
		t.Errorf("line: %s", got)
	}
}
