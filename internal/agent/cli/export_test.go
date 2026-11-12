package cli

import (
	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/agent/config"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

func LauncherCmd(
	doer wire.HttpRequestDoer,
	engine func(config.Docker) (Engine, error),
) *cobra.Command {
	return launcherCmd(deps{doer: doer, engine: engine})
}

func RunnerCmd(doer wire.HttpRequestDoer) *cobra.Command { return runnerCmd(doer) }
