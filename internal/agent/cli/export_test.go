package cli

import (
	"crypto/x509"

	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"

	"github.com/weaveplatform/infraweave/internal/agent/bootstrap"
	"github.com/weaveplatform/infraweave/internal/agent/config"
	"github.com/weaveplatform/infraweave/internal/agent/runner"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

func LauncherCmd(doer wire.HttpRequestDoer, engine func(config.Docker) (Engine, error)) *cobra.Command {
	return launcherCmd(deps{doer: doer, engine: engine})
}

func RunnerCmd(doer wire.HttpRequestDoer) *cobra.Command {
	return runnerCmd(func(*x509.CertPool) wire.HttpRequestDoer { return doer }, bootstrap.PodPaths, runner.Interrupt)
}

func PodRunnerCmd(paths bootstrap.Paths, interrupt func(int) error) *cobra.Command {
	return runnerCmd(nil, paths, interrupt)
}

func WorkerControllerCmd(kube func() (*rest.Config, error)) *cobra.Command {
	return workerControllerCmd(kube)
}

func KubeConfig() (*rest.Config, error) { return kubeconfig() }
