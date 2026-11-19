// Command vcs-lab manages only the scheduled verification lab's resources.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/vcslab"
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
	root := &cobra.Command{Use: "vcs-lab", Short: "Manage verification-only VCS lab resources", SilenceUsage: true, SilenceErrors: true}
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
			for _, file := range files {
				if err := vcslab.RestoreHook(cmd.Context(), client, file); err != nil {
					return err
				}
			}
			return nil
		},
	}
	restore.Flags().StringVar(&directory, "journal-directory", "", "Private lab hook cleanup directory")
	root.AddCommand(setup, restore)
	return root
}
