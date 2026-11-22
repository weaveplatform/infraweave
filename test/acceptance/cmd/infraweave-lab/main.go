// Command infraweave-lab manages only the scheduled verification lab's resources.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/vcslab"
	"github.com/weaveplatform/infraweave/test/deps"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := command().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func command() *cobra.Command {
	root := &cobra.Command{Use: "infraweave-lab", Short: "Manage verification-only acceptance lab resources", SilenceUsage: true, SilenceErrors: true}
	var secretFile string
	setup := &cobra.Command{
		Use:   "app-setup",
		Short: "Write the derived App hook secret to a new private file; make no API calls",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if secretFile == "" {
				return fmt.Errorf("%w: --secret-file is required", vcslab.ErrHook)
			}
			return vcslab.WriteAppSetup(secretFile, os.Getenv("INFRAWEAVE_TEST_VCS_USER"), []byte(os.Getenv("INFRAWEAVE_TEST_VCS_TOKEN")), cmd.OutOrStdout())
		},
	}
	setup.Flags().StringVar(&secretFile, "secret-file", "", "New0600 file for the one-time operator setup")
	readiness := &cobra.Command{
		Use:   "app-readiness",
		Short: "Validate the operator setup attestation without an API call",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			key, err := vcslab.DeriveAppKey(os.Getenv("LAB_GITHUB_APP_ID"), []byte(os.Getenv("LAB_GITHUB_APP_PRIVATE_KEY")))
			if err != nil {
				return err
			}
			if ready := os.Getenv("INFRAWEAVE_LAB_GITHUB_READY"); ready == "" || ready != key.Readiness {
				return fmt.Errorf("%w: dedicated App setup readiness is missing or stale", vcslab.ErrHook)
			}
			if _, err := vcslab.ParkingConfig(os.Getenv("INFRAWEAVE_LAB_GITHUB_PARKED_URL")); err != nil {
				return err
			}
			return nil
		},
	}
	var directory string
	restore := &cobra.Command{
		Use:   "restore-app",
		Short: "Restore owned App hook journals after a failed lab",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if directory == "" {
				return fmt.Errorf("%w: --journal-directory is required", vcslab.ErrHook)
			}
			files, err := filepath.Glob(filepath.Join(directory, "app-hook-*.json"))
			if err != nil {
				return fmt.Errorf("%w: list cleanup journals", vcslab.ErrHook)
			}
			if len(files) == 0 {
				return nil
			}
			client, err := vcslab.NewAppHooks(os.Getenv("INFRAWEAVE_TEST_VCS_USER"), []byte(os.Getenv("INFRAWEAVE_TEST_VCS_TOKEN")))
			if err != nil {
				return err
			}
			var failures error
			for _, file := range files {
				failures = errors.Join(failures, vcslab.RestoreHook(cmd.Context(), client, file))
			}
			return failures
		},
	}
	restore.Flags().StringVar(&directory, "journal-directory", "", "Private lab hook cleanup directory")
	var destination string
	install := &cobra.Command{
		Use:   "install-tunnel",
		Short: "Install the checksum-pinned Linux quick-tunnel binary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (result error) {
			if runtime.GOOS != "linux" || destination == "" {
				return fmt.Errorf("%w: Linux and --destination are required", vcslab.ErrTunnel)
			}
			d := &deps.Dependencies{}
			defer func() {
				if err := d.Close(context.WithoutCancel(cmd.Context())); err != nil {
					result = errors.Join(result, vcslab.ErrTunnel)
				}
			}()
			path, err := d.Tool(cmd.Context(), deps.Cloudflared, runtime.GOARCH)
			if err != nil {
				return vcslab.ErrTunnel
			}
			in, err := os.Open(path)
			if err != nil {
				return vcslab.ErrTunnel
			}
			defer func() { _ = in.Close() }()
			out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
			if err != nil {
				return vcslab.ErrTunnel
			}
			_, copyErr := io.Copy(out, in)
			closeErr := out.Close()
			if copyErr != nil || closeErr != nil {
				return vcslab.ErrTunnel
			}
			return nil
		},
	}
	install.Flags().StringVar(&destination, "destination", "", "New file for the verified executable")
	var binary, origin, session string
	tunnel := &cobra.Command{Use: "tunnel", Short: "Run one ephemeral account-free tunnel until signaled", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if binary == "" || origin == "" || session == "" {
			return vcslab.ErrTunnel
		}
		process, err := vcslab.TunnelCommand(cmd.Context(), binary, origin, session)
		if err != nil {
			return err
		}
		return vcslab.RunTunnelSession(cmd.Context(), process, session)
	}}
	tunnel.Flags().StringVar(&binary, "binary", "", "Checksum-verified cloudflared executable")
	tunnel.Flags().StringVar(&origin, "origin", "", "Local webhook ingress origin")
	tunnel.Flags().StringVar(&session, "session-directory", "", "Fresh private directory for this tunnel session")
	urlCommand := &cobra.Command{Use: "tunnel-url", Short: "Read the established current-session URL", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		value, err := vcslab.TunnelURL(session)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), value)
		return err
	}}
	urlCommand.Flags().StringVar(&session, "session-directory", "", "Current private tunnel session")
	stopCommand := &cobra.Command{
		Use:   "stop-tunnel",
		Short: "Request an owned stop and verify the joined terminal outcome",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return vcslab.StopTunnel(cmd.Context(), session) },
	}
	stopCommand.Flags().StringVar(&session, "session-directory", "", "Current private tunnel session")
	var pkiDirectory string
	pki := &cobra.Command{Use: "pki", Short: "Create an isolated FIPS-compatible lab TLS fixture", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		if pkiDirectory == "" {
			return fmt.Errorf("%w: --directory is required", deps.ErrLab)
		}
		return deps.WriteLabPKI(pkiDirectory)
	}}
	pki.Flags().StringVar(&pkiDirectory, "directory", "", "New private batch PKI directory")
	var manifest string
	verify := &cobra.Command{
		Use:   "verify-lab",
		Short: "Verify owned Terraform services and actual Kubernetes readiness without provisioning",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if manifest == "" {
				return fmt.Errorf("%w: --manifest is required", deps.ErrLab)
			}
			d := &deps.Dependencies{}
			return d.UseLab(cmd.Context(), manifest)
		},
	}
	verify.Flags().StringVar(&manifest, "manifest", "", "Private Terraform acceptance manifest")
	root.AddCommand(setup, readiness, restore, install, tunnel, urlCommand, stopCommand, pki, verify)
	return root
}
