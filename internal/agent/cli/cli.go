package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/agent/config"
	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/executor/docker"
	"github.com/weaveplatform/infraweave/internal/agent/launcher"
	"github.com/weaveplatform/infraweave/internal/agent/runner"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/platform/buildinfo"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/serve"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
)

// Engine is an executor the admin listener's readiness checks, and which
// the launcher closes when it exits.
type Engine interface {
	executor.Executor
	Ping(ctx context.Context) error
	Close() error
}

// deps are what the commands run on: the worker API's HTTP client and the
// executor of the launcher's settings. A nil doer is the client
// launcher.New builds from them.
type deps struct {
	doer   wire.HttpRequestDoer
	engine func(config.Docker) (Engine, error)
}

// shutdownTimeout bounds the admin listener's drain; no load balancer
// routes to a launcher, so its listener shuts down at once.
const shutdownTimeout = 5 * time.Second

// runnerTimeout bounds each of the runner's calls.
const runnerTimeout = 30 * time.Second

// Launcher is infraweave launcher, which executes a private pool's runs in
// Docker containers.
func Launcher() *cobra.Command {
	return launcherCmd(deps{
		engine: func(c config.Docker) (Engine, error) {
			return docker.New(docker.Config{
				Binary: c.RunnerBinary, Network: c.Network, Runtime: c.Runtime,
			})
		},
	})
}

func launcherCmd(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "launcher",
		Short: "Register a worker of a private pool and execute its runs in Docker containers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return launch(cmd, d)
		},
	}
	config.Flags(cmd.Flags())
	return cmd
}

// launch binds the admin listener, then runs the launcher until the
// command's context ends or a cycle ends it. SIGHUP re-reads
// launcher.log.level and nothing else.
func launch(cmd *cobra.Command, d deps) error {
	c, err := config.Load(cmd.Flags())
	if err != nil {
		return err //nolint:wrapcheck // config names the setting
	}
	lc := c.Launcher
	log, level, err := observe.NewLogger(cmd.OutOrStdout(), lc.Log, "launcher")
	if err != nil {
		return err //nolint:wrapcheck // observe names the setting
	}
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go observe.FollowLevel(ctx, hup, func() (slog.Level, error) {
		c, err := config.Load(cmd.Flags())
		if err != nil {
			return 0, err //nolint:wrapcheck // config names the setting
		}
		return observe.ParseLevel(c.Launcher.Log.Level)
	}, level, log)

	e, err := d.engine(lc.Docker)
	if err != nil {
		return fmt.Errorf("launcher: docker: %w", err)
	}
	defer func() { _ = e.Close() }()
	l, err := launcher.New(launcher.Deps{
		Config: lc, Doer: d.doer, Executor: e, Log: log,
		Version: buildinfo.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
	})
	if err != nil {
		return err //nolint:wrapcheck // launcher names the setting
	}
	h := observe.NewHealth(time.Now)
	h.Startup("registered", l.Registered)
	h.Startup("docker", e.Ping)
	h.Ready("polling", l.Ready)
	h.Ready("docker", e.Ping)
	srv, err := serve.Start(ctx, serve.Config{
		Listen: serve.Defaults().Listen, AdminListen: lc.AdminListen,
		Shutdown: serve.Shutdown{Timeout: shutdownTimeout}, Timeouts: serve.DefaultTimeouts(),
	}, log, nil, observe.Probes(log, h))
	if err != nil {
		return err //nolint:wrapcheck // serve names the listener
	}
	served := make(chan error, 1)
	go func() {
		served <- srv.Wait(ctx, log, h.Stop)
		cancel()
	}()
	err = l.Run(ctx)
	cancel()
	return errors.Join(err, <-served)
}

// Runner is infraweave runner, the root process of a run's container,
// which the launcher starts; it is hidden, as no one else runs it.
func Runner() *cobra.Command {
	return runnerCmd(&http.Client{Timeout: runnerTimeout})
}

func runnerCmd(doer wire.HttpRequestDoer) *cobra.Command {
	return &cobra.Command{
		Use:    "runner",
		Short:  "Execute a run's phase from the launcher's handoff on standard input",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// NewLogger fails only on settings LogDefaults does not have.
			log, _, _ := observe.NewLogger(cmd.OutOrStdout(), observe.LogDefaults(), "runner")
			return runner.Run(
				cmd.Context(),
				cmd.InOrStdin(),
				doer,
				log,
			) //nolint:wrapcheck // runner names the step
		},
	}
}
