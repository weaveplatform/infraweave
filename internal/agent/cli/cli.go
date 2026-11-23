package cli

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/agent/bootstrap"
	"github.com/weaveplatform/infraweave/internal/agent/config"
	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/executor/docker"
	"github.com/weaveplatform/infraweave/internal/agent/initcommand"
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
	l, err := launcher.NewContext(ctx, launcher.Deps{
		Config: lc, Doer: d.doer, Executor: e, Log: log,
		Version: buildinfo.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		Inputs: launcher.Host{Config: lc},
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
	return runnerCmd(runner.Client(runnerTimeout), bootstrap.PodPaths, runner.Interrupt)
}

// runnerCmd is infraweave runner calling the worker API through the
// client doer makes, its
// bootstrap writing to paths, and its signal sending through interrupt.
func runnerCmd(doer func(*x509.CertPool) wire.HttpRequestDoer, paths bootstrap.Paths, interrupt func(int) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "runner",
		Short:  "Execute a run's phase from the launcher's handoff on standard input",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// NewLogger fails only on settings LogDefaults does not have.
			log, _, _ := observe.NewLogger(cmd.OutOrStdout(), observe.LogDefaults(), "runner")
			// The launcher's SIGINT stops the run's command, not the runner,
			// so the root command's shutdown handler gives it up.
			stop := make(chan os.Signal, 1)
			signal.Reset(os.Interrupt)
			signal.Notify(stop, os.Interrupt)
			defer signal.Stop(stop)
			return runner.Run(
				cmd.Context(),
				cmd.InOrStdin(),
				doer,
				log,
				stop,
			) //nolint:wrapcheck // runner names the step
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "hand-back",
		Short: "Open the workspace of a run whose container ended first to the launcher's user",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return runner.HandBack() },
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "command",
		Short: "Execute an initialization policy's command from the private request on standard input",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return initcommand.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout()) //nolint:wrapcheck // initcommand names the step
		},
	})
	cmd.AddCommand(bootstrapCmd(paths), &cobra.Command{
		Use:       "signal interrupt",
		Short:     "Send the run container's runner, its process 1, SIGINT",
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"interrupt"},
		RunE:      func(*cobra.Command, []string) error { return interrupt(1) },
	})
	return cmd
}

// bootstrapCmd is infraweave runner bootstrap, a run Pod's init container,
// which receives the Pod's preparation stream on standard input into paths.
func bootstrapCmd(paths bootstrap.Paths) *cobra.Command {
	var want bootstrap.Expect
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Receive a run Pod's workspace, tools and runner from standard input",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			self, err := os.Executable()
			if err != nil {
				return fmt.Errorf("bootstrap: find its own executable: %w", err)
			}
			m, err := bootstrap.Receive(cmd.InOrStdin(), paths, want, runtime.GOARCH, self)
			if err != nil {
				return err //nolint:wrapcheck // bootstrap names the step
			}
			// The Pod's log carries only the stream's shape, never its
			// content.
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "received run %s at state version %d: workspace %d bytes, %d tools\n",
				m.Run, m.StateVersion, m.Workspace.Size, len(m.Tools))
			return err //nolint:wrapcheck // standard output's own error
		},
	}
	f := cmd.Flags()
	f.StringVar(&want.Run, "run", "", "the run the stream must be for")
	f.Int64Var(&want.StateVersion, "state-version", 0, "the run's state version the stream must be for")
	f.StringVar(&want.Worker, "worker", "", "the worker the stream must be for")
	for _, name := range []string{"run", "state-version", "worker"} {
		// MarkFlagRequired fails only for a flag that does not exist.
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}
