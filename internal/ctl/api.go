package ctl

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/platform/buildinfo"
	"github.com/weaveplatform/infraweave/pkg/sdk"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit and build date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "infraweavectl %s (commit %s, built %s)\n",
				buildinfo.Version(), orUnknown(buildinfo.Commit()),
				orUnknown(buildinfo.BuildDate())); err != nil {
				return fmt.Errorf("write: %w", err)
			}
			return nil
		},
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Print the claims of the credential's token",
		Long: "Print the claims of the token the credential stands for: the API key's, " +
			"exchanged at the server, which checks the key, or the token as it is.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := target(cmd.Flags())
			if err != nil {
				return err
			}
			token := p.Token
			if p.Key != "" {
				if token, err = exchange(cmd, p); err != nil {
					return err
				}
			}
			if token == "" {
				return fmt.Errorf("%w: set an API key or a token", ErrNoCredential)
			}
			return claims(cmd, token)
		},
	}
}

// exchange is the token p's API key exchanges for.
func exchange(cmd *cobra.Command, p Profile) (string, error) {
	c, err := sdk.New(p.Endpoint)
	if err != nil {
		return "", err //nolint:wrapcheck // the SDK names itself
	}
	r, err := c.Do(cmd.Context(), http.MethodPost, sdk.ExchangePath,
		map[string]string{"id": p.Key, "secret": p.Secret})
	if err != nil {
		return "", err //nolint:wrapcheck // the SDK's typed problem, as it is
	}
	var u struct {
		JWT string `json:"jwt"`
	}
	if err := json.Unmarshal(r.Body, &u); err != nil || u.JWT == "" {
		return "", fmt.Errorf("exchange API key %s: %w", p.Key, sdk.ErrNoToken)
	}
	return u.JWT, nil
}

// claims prints a JWT's claims, decoded without verifying its signature,
// which only the server can.
func claims(cmd *cobra.Command, token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("%w: the token is not a JWT", ErrInput)
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !json.Valid(b) {
		return fmt.Errorf("%w: the token's claims do not decode", ErrInput)
	}
	return show(cmd.OutOrStdout(), b)
}

func apiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api METHOD PATH",
		Short: "Send any request and print the answer",
		Long: "Send METHOD to PATH, which starts at the server's root (/api/v1/..., " +
			"/operator/v1/...), with the profile's credential, and print the answer. " +
			"--data is the JSON body, @FILE reads it from a file and - from standard input.",
		Example: "  infraweavectl api GET /api/v1/viewer\n" +
			"  infraweavectl api POST /operator/v1/tiers --data @tier.json",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var body any
			if data, _ := cmd.Flags().GetString("data"); data != "" {
				b, err := payload(cmd, data)
				if err != nil {
					return err
				}
				body = json.RawMessage(b)
			}
			raw, _ := cmd.Flags().GetStringArray("header")
			headers := make([]sdk.Header, 0, len(raw))
			for _, h := range raw {
				name, value, ok := strings.Cut(h, ":")
				if !ok || strings.TrimSpace(name) == "" {
					return fmt.Errorf("%w: header %q is not NAME: VALUE", ErrInput, h)
				}
				headers = append(headers, sdk.Header{
					Name: strings.TrimSpace(name), Value: strings.TrimSpace(value),
				})
			}
			return call(cmd, strings.ToUpper(args[0]), args[1], body, headers...)
		},
	}
	cmd.Flags().String("data", "", "JSON body, @FILE or - for standard input")
	cmd.Flags().StringArrayP("header", "H", nil, "request header NAME: VALUE, repeatable")
	return cmd
}

// payload is --data's JSON: the text itself, a file's, or standard input's.
func payload(cmd *cobra.Command, data string) ([]byte, error) {
	var b []byte
	var err error
	switch {
	case data == "-":
		b, err = io.ReadAll(cmd.InOrStdin())
	case strings.HasPrefix(data, "@"):
		b, err = os.ReadFile(data[1:])
	default:
		b = []byte(data)
	}
	if err != nil {
		return nil, fmt.Errorf("read --data: %w", err)
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("%w: --data is not JSON", ErrInput)
	}
	return b, nil
}
