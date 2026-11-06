package main

import (
	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/app"
)

func authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Produce credentials for the operator API",
		Args:  cobra.NoArgs,
	}
	hash := &cobra.Command{
		Use:   "hash-password",
		Short: "Print the break-glass hash of the password read on standard input",
		Long: "Reads the password as the first line of standard input, so it never appears in a " +
			"process list or shell history, and prints its PBKDF2-HMAC-SHA256 hash for an " +
			"organization's break-glass credential.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.HashPassword(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	cmd.AddCommand(hash)
	return cmd
}
