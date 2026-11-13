package runner

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/exec"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// Run reads the launcher's handoff from in and executes the run it hands
// over in the working directory, the container's workspace, reporting
// and uploading its log through doer. The phases' masked output goes to
// standard output, which the launcher reads, too.
//
// ref: decision 0008 § "The launcher and the runner"
func Run(ctx context.Context, in io.Reader, doer wire.HttpRequestDoer, log *slog.Logger) error {
	h, err := handoff.Read(in)
	if err != nil {
		return err //nolint:wrapcheck // handoff names the read
	}
	c := client.New(h.Endpoint, doer)
	return exec.Run(ctx, h, exec.Deps{ //nolint:wrapcheck // exec names the step
		Reports:   c,
		Logs:      c,
		Uploads:   c,
		Log:       log,
		Out:       os.Stdout,
		Workspace: ".",
		Environ:   os.Environ(),
	})
}
