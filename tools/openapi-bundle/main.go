// Command openapi-bundle merges api/src/<service>/*.yaml fragments into
// api/<service>.openapi.yaml (make bundle). Logic lives in
// internal/openapibundle.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/weaveplatform/infraweave/internal/buildinfo"
	"github.com/weaveplatform/infraweave/internal/openapibundle"
)

// config is every setting, from flag > OPENAPI_BUNDLE_* environment >
// --config file.
type config struct {
	Src string `mapstructure:"src"`
	Out string `mapstructure:"out"`
}

func main() {
	if newRoot().Execute() != nil {
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	v := viper.New()
	var cfgFile string
	root := &cobra.Command{
		Use:          "openapi-bundle",
		Short:        "Bundle api/src/<service>/ fragments into api/<service>.openapi.yaml",
		Version:      buildinfo.Version(),
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := load(v, cmd, cfgFile)
			if err != nil {
				return err
			}
			done, err := openapibundle.BundleAll(c.Src, c.Out)
			for _, s := range done {
				cmd.Printf("bundled %s -> %s/%s.openapi.yaml\n", s, c.Out, s)
			}
			return err
		},
	}
	f := root.Flags()
	f.StringVar(&cfgFile, "config", "", "YAML config file (unknown keys are rejected)")
	f.String("src", "api/src", "directory holding one sub-directory per service")
	f.String("out", "api", "directory receiving <service>.openapi.yaml")
	return root
}

func load(v *viper.Viper, cmd *cobra.Command, cfgFile string) (*config, error) {
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
		return nil, fmt.Errorf("bind flags: %w", bindErr)
	}
	v.SetEnvPrefix("OPENAPI_BUNDLE")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
		v.SetConfigType("yaml")
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}
	var c config
	if err := v.UnmarshalExact(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &c, nil
}
