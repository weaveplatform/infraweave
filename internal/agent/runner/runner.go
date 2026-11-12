package runner

import (
	"context"
	"io"
	"log/slog"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// Detail is the failed report's details, which the run's transition
// records as its note.
const Detail = "This build's runner executes no phase, so the run ends here."

// Run reads the launcher's handoff from in, reports that the run's first
// phase started, which ends the handover, and then that it failed. This
// build's runner executes no phase; failing the run says so in its note,
// where a runner that exited without a report would leave the run to the
// lost-run timeout, and one that reported success would claim work it did
// not do.
//
// ref: decision 0008 § "The launcher and the runner"
func Run(ctx context.Context, in io.Reader, doer wire.HttpRequestDoer, log *slog.Logger) error {
	h, err := handoff.Read(in)
	if err != nil {
		return err //nolint:wrapcheck // handoff names the read
	}
	c := client.New(h.Endpoint, doer)
	state := h.First()
	step, err := c.Report(ctx, h.RunToken, h.Run, wire.RunReport{
		StateVersion: h.StateVersion, State: state, Event: wire.Started,
	})
	if err != nil {
		return err //nolint:wrapcheck // client names the report
	}
	log.InfoContext(ctx, "phase started", "run", h.Run, "state", state)
	details := Detail
	step, err = c.Report(ctx, h.RunToken, h.Run, wire.RunReport{
		StateVersion: step.StateVersion, State: state, Event: wire.Failed, Details: &details,
	})
	if err != nil {
		return err //nolint:wrapcheck // client names the report
	}
	log.InfoContext(ctx, "run ended", "run", h.Run, "state", step.State, "action", step.Action)
	return nil
}
