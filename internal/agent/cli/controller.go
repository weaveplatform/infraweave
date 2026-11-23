package cli

import (
	"context"
	"crypto/fips140"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/weaveplatform/infraweave/internal/agent/controller"
	"github.com/weaveplatform/infraweave/internal/platform/buildinfo"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/serve"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
)

// kubeconfig is the Kubernetes client configuration: the file KUBECONFIG
// names, as for a controller run outside its cluster, or else the
// service account the Pod mounts.
func kubeconfig() (*rest.Config, error) {
	if path := os.Getenv("KUBECONFIG"); path != "" {
		c, err := clientcmd.BuildConfigFromFlags("", path)
		if err != nil {
			return nil, fmt.Errorf("worker controller: KUBECONFIG: %w", err)
		}
		return c, nil
	}
	c, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("worker controller: in-cluster configuration: %w", err)
	}
	return c, nil
}

// WorkerController is infraweave worker-controller, which reconciles the
// WorkerPool and Worker resources of its watched namespaces.
func WorkerController() *cobra.Command { return workerControllerCmd(kubeconfig) }

func workerControllerCmd(kube func() (*rest.Config, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker-controller",
		Short: "Reconcile Kubernetes WorkerPool and Worker resources through the worker and account APIs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings, err := controller.LoadSettings(cmd.Flags())
			if err != nil {
				return err //nolint:wrapcheck // controller names the setting
			}
			log, _, err := observe.NewLogger(cmd.OutOrStdout(), settings.Log, "worker-controller")
			if err != nil {
				return err //nolint:wrapcheck // observe names the setting
			}
			log.Info("worker controller starting", provenance()...)
			config, err := kube()
			if err != nil {
				return err
			}
			clients, err := controller.NewClients(config)
			if err != nil {
				return err //nolint:wrapcheck // controller names the client
			}
			host, _ := os.Hostname()
			suffix := make([]byte, 4)
			_, _ = rand.Read(suffix) // crypto/rand.Read never fails
			c, err := controller.New(controller.Deps{
				Settings: settings, Clients: clients, Log: log,
				Version: buildinfo.Version(), Arch: runtime.GOARCH,
				Identity: host + "-" + hex.EncodeToString(suffix),
			})
			if err != nil {
				return err //nolint:wrapcheck // controller names the setting
			}
			ctx := cmd.Context()
			h := observe.NewHealth(time.Now)
			h.Ready("kubernetes", c.Reachable)
			h.Ready("synced", c.Synced)
			h.Ready("installation", c.Connected)
			srv, err := serve.Start(ctx, serve.Config{
				Listen: serve.Defaults().Listen, AdminListen: settings.AdminListen,
				Shutdown: serve.Shutdown{Timeout: shutdownTimeout}, Timeouts: serve.DefaultTimeouts(),
			}, log, nil, observe.Probes(log, h))
			if err != nil {
				return err //nolint:wrapcheck // serve names the listener
			}
			err = c.Run(ctx)
			return errors.Join(err, srv.Wait(ctx, log, h.Stop))
		},
	}
	controller.ConfigurationFlags(cmd.Flags())
	cmd.AddCommand(uninstallCmd(kube))
	return cmd
}

// uninstallCmd is infraweave worker-controller uninstall, which an
// installation runs before it removes its controller, so that the
// controller still finalizes the installation's WorkerPools.
func uninstallCmd(kube func() (*rest.Config, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Delete the installation's WorkerPools and wait while the running controller finalizes them",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings, err := controller.LoadSettings(cmd.Flags())
			if err != nil {
				return err //nolint:wrapcheck // controller names the setting
			}
			log, _, err := observe.NewLogger(cmd.OutOrStdout(), settings.Log, "worker-controller")
			if err != nil {
				return err //nolint:wrapcheck // observe names the setting
			}
			config, err := kube()
			if err != nil {
				return err
			}
			clients, err := controller.NewClients(config)
			if err != nil {
				return err //nolint:wrapcheck // controller names the client
			}
			selector, _ := cmd.Flags().GetString("selector")
			timeout, _ := cmd.Flags().GetDuration("timeout")
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			return controller.Uninstall(
				ctx,
				clients.Dynamic,
				settings.WatchNamespaces,
				selector,
				controller.UninstallInterval,
				log,
			) //nolint:wrapcheck // controller names the pools
		},
	}
	controller.ConfigurationFlags(cmd.Flags())
	cmd.Flags().String("selector", "", "label selector of the WorkerPools the installation owns")
	cmd.Flags().Duration("timeout", 10*time.Minute, "how long to wait for the pools to be finalized")
	return cmd
}

// provenance is what the controller logs of its build as it starts: its
// commit and its FIPS 140-3 mode and module, beside the version every
// line carries.
func provenance() []any {
	mode := "off"
	switch {
	case fips140.Enforced():
		mode = "only"
	case fips140.Enabled():
		mode = "on"
	}
	return []any{"commit", buildinfo.Commit(), "fips140", mode, "fips140_module", fips140.Version()}
}
