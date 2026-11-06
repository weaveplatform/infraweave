package main

import (
	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/app"
	"github.com/weaveplatform/infraweave/internal/app/config"
)

func secretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Generate a local KEK, rotate the data key, or rewrap the data keys to another KEK",
		Args:  cobra.NoArgs,
	}
	generate := &cobra.Command{
		Use:   "generate-kek <file>",
		Short: "Write a new local KEK to a file that does not exist yet, readable only by its owner",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return app.GenerateKEK(args[0])
		},
	}
	rotate := &cobra.Command{
		Use:   "rotate",
		Short: "Make a new active data key now; a scheduler reseals every value under it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := config.Load(cmd.Flags())
			if err != nil {
				return err
			}
			return app.SecretsRotate(cmd.Context(), *c, cmd.OutOrStdout())
		},
	}
	rewrap := &cobra.Command{
		Use:   "rewrap --to <file>",
		Short: "Rewrap every data key from the configured KEK to the one a file of secrets.kek settings names",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := config.Load(cmd.Flags())
			if err != nil {
				return err
			}
			file, _ := cmd.Flags().GetString("to")
			to, err := config.LoadKEK(file)
			if err != nil {
				return err
			}
			return app.SecretsRewrap(cmd.Context(), *c, to, cmd.OutOrStdout())
		},
	}
	config.FileFlag(rotate.Flags())
	config.FileFlag(rewrap.Flags())
	// --to names a file, not a setting, so config.Load never binds it.
	rewrap.Flags().String("to", "", "YAML file of the secrets.kek settings to rewrap to")
	_ = rewrap.MarkFlagRequired("to") // the flag is defined on the line above
	cmd.AddCommand(generate, rotate, rewrap)
	return cmd
}
