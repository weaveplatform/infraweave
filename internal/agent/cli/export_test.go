package cli

import (
	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"

	"github.com/weaveplatform/infraweave/internal/agent/config"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

func LauncherCmd(doer wire.HttpRequestDoer, engine func(config.Docker) (Engine, error)) *cobra.Command {
	return launcherCmd(deps{doer: doer, engine: engine})
}

func RunnerCmd(doer wire.HttpRequestDoer) *cobra.Command { return runnerCmd(doer) }

func WorkerControllerCmd(kube func() (*rest.Config, error)) *cobra.Command {
	return workerControllerCmd(kube)
}

func KubeConfig() (*rest.Config, error) { return kubeconfig() }
