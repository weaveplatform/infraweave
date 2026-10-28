// Command specref maintains the Spacelift API reference in
// api/reference/spacelift: fetch refreshes the pinned upstream snapshots,
// generate derives the OpenAPI spec and coverage report from them, and check
// fails when the committed output is stale. Logic lives in internal/specref.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/internal/buildinfo"
	"github.com/weaveplatform/infraweave/internal/specref"
)

// Pins used when no sources.lock.yaml exists yet. Once one does, its values
// are the defaults, so a local fetch never silently moves a pin that a
// refresh PR set.
const (
	defaultEndpoint        = "https://demo.app.spacelift.io/graphql"
	defaultBrunoRepo       = "spacelift-io/spacelift-api-bruno"
	defaultBrunoRef        = "7cad4574954b569df083b2a809b68371fc385e6d"
	defaultProviderRepo    = "spacelift-io/terraform-provider-spacelift"
	defaultProviderVersion = "1.57.0"
)

// config is every setting, from flag > SPECREF_* environment > --config file.
type config struct {
	Dir             string `mapstructure:"dir"`
	Endpoint        string `mapstructure:"endpoint"`
	Codeload        string `mapstructure:"codeload"`
	BrunoRepo       string `mapstructure:"bruno-repo"`
	BrunoRef        string `mapstructure:"bruno-ref"`
	ProviderRepo    string `mapstructure:"provider-repo"`
	ProviderVersion string `mapstructure:"provider-version"`
	Terraform       string `mapstructure:"terraform"`
	Timeout         string `mapstructure:"timeout"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := newRoot().ExecuteContext(ctx)
	stop()
	if err != nil {
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	v := viper.New()
	var cfgFile string
	root := &cobra.Command{
		Use:           "specref",
		Short:         "Maintain the Spacelift API reference (GraphQL → REST OpenAPI 3)",
		Version:       buildinfo.Version(),
		SilenceUsage:  true,
		SilenceErrors: false,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return loadConfig(v, cmd, cfgFile)
		},
	}
	pf := root.PersistentFlags()
	pf.StringVar(&cfgFile, "config", "", "YAML config file (unknown keys are rejected)")
	pf.String("dir", filepath.Join("api", "reference", "spacelift"), "reference directory")

	fetch := &cobra.Command{
		Use:   "fetch",
		Short: "Refresh the upstream snapshots and sources.lock.yaml (network, terraform)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := decode(v)
			if err != nil {
				return err
			}
			timeout, err := time.ParseDuration(c.Timeout)
			if err != nil {
				return fmt.Errorf("timeout: %w", err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			return specref.Fetch(ctx, specref.FetchOptions{
				Dir: c.Dir, Endpoint: c.Endpoint, Codeload: c.Codeload,
				BrunoRepo: c.BrunoRepo, BrunoRef: c.BrunoRef,
				ProviderRepo: c.ProviderRepo, ProviderVersion: c.ProviderVersion,
				Terraform: c.Terraform, Run: specref.ExecRunner, Now: time.Now,
				Fetcher: &specref.Fetcher{
					Client:    &http.Client{Timeout: 2 * time.Minute},
					UserAgent: "infraweave-specref/" + buildinfo.Version() + " (+https://github.com/weaveplatform/infraweave)",
					Retries:   3,
					Backoff:   2 * time.Second,
				},
			})
		},
	}
	f := fetch.Flags()
	f.String("endpoint", defaultEndpoint, "GraphQL endpoint to introspect (needs no credentials)")
	f.String("codeload", "https://codeload.github.com", "base URL serving <repo>/tar.gz/<ref>")
	f.String("bruno-repo", defaultBrunoRepo, "spacelift-api-bruno repository")
	f.String("bruno-ref", defaultBrunoRef, "spacelift-api-bruno commit SHA")
	f.String("provider-repo", defaultProviderRepo, "terraform-provider-spacelift repository")
	f.String(
		"provider-version",
		defaultProviderVersion,
		"terraform-provider-spacelift release, without the v",
	)
	f.String("terraform", "terraform", "terraform binary used to read the provider schema")
	f.String("timeout", "10m", "overall fetch timeout")

	generate := &cobra.Command{
		Use:   "generate",
		Short: "Write spacelift.openapi.yaml and coverage.md from the snapshots",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := decode(v)
			if err != nil {
				return err
			}
			g, err := specref.GenerateDir(cmd.Context(), c.Dir)
			if err != nil {
				return err
			}
			routed := 0
			for _, n := range g.Report.Styles {
				routed += n
			}
			cmd.Printf(
				"%d routes (+%d field routes), %d excluded, %d RPC-shaped\n",
				routed,
				g.Report.FieldRoutes,
				len(g.Report.Excluded),
				len(g.Report.RPC),
			)
			return nil
		},
	}
	check := &cobra.Command{
		Use:   "check",
		Short: "Fail if the committed spec or report is stale",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := decode(v)
			if err != nil {
				return err
			}
			return specref.CheckDir(cmd.Context(), c.Dir)
		},
	}
	root.AddCommand(fetch, generate, check)
	return root
}

// loadConfig layers settings flag > env > file > lock pins > defaults.
func loadConfig(v *viper.Viper, cmd *cobra.Command, cfgFile string) error {
	// Bind settings only: --config, --help and --version are not settings,
	// and UnmarshalExact would reject them as unknown keys.
	var bindErr error
	cmd.Flags().VisitAll(func(fl *pflag.Flag) {
		if fl.Name == "config" || fl.Name == "help" || fl.Name == "version" {
			return
		}
		if err := v.BindPFlag(fl.Name, fl); err != nil {
			bindErr = errors.Join(bindErr, err)
		}
	})
	if bindErr != nil {
		return fmt.Errorf("bind flags: %w", bindErr)
	}
	v.SetEnvPrefix("SPECREF")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
		v.SetConfigType("yaml")
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("read config: %w", err)
		}
	}
	lock, err := readLock(v.GetString("dir"))
	if err != nil {
		return err
	}
	if lock != nil {
		// Viper ranks SetDefault under a changed flag, env and the config
		// file but above an unchanged flag's own default, so the lock fills
		// exactly what nobody set explicitly.
		v.SetDefault("endpoint", lock.Introspection.Endpoint)
		v.SetDefault("bruno-repo", lock.Bruno.Repository)
		v.SetDefault("bruno-ref", lock.Bruno.Ref)
		v.SetDefault("provider-repo", lock.Provider.Repository)
		v.SetDefault("provider-version", strings.TrimPrefix(lock.Provider.Ref, "v"))
	}
	return nil
}

func readLock(dir string) (*specref.Lock, error) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil //nolint:nilnil // no reference directory yet: no pins
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	defer root.Close()
	data, err := root.ReadFile(specref.FileLock)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil //nolint:nilnil // first fetch: no pins yet
	}
	if err != nil {
		return nil, fmt.Errorf("read lock: %w", err)
	}
	var lock specref.Lock
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("decode lock: %w", err)
	}
	return &lock, nil
}

// decode applies the layered settings strictly: a config file key that
// matches no setting is an error, not a silent no-op.
func decode(v *viper.Viper) (*config, error) {
	var c config
	if err := v.UnmarshalExact(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &c, nil
}
