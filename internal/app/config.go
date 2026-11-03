// Package app wires infraweave's process modes: the configuration they read
// and, as domains arrive, the router and consumers they compose.
// cmd/infraweave holds one thin cobra file per process mode over it.
//
// Settings come from flags, INFRAWEAVE_* environment variables and a YAML
// file named by --config, in that order of precedence. The file is strict:
// a key infraweave does not know fails the command rather than being
// ignored, so a misspelt setting never silently falls back to its default.
package app

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/weaveplatform/infraweave/internal/buildinfo"
	"github.com/weaveplatform/infraweave/internal/server"
)

// Config is every setting, keyed as in the YAML file.
type Config struct {
	Server server.Config `mapstructure:"server"`
}

// ConfigFlag names the YAML file. It is not a setting, so it is never bound.
const ConfigFlag = "config"

// settingFlags maps each setting flag to its configuration key. Only these
// are bound to viper: --config and --help are not settings, and
// UnmarshalExact would reject them as unknown keys.
var settingFlags = map[string]string{
	"listen":           "server.listen",
	"admin-listen":     "server.admin_listen",
	"shutdown-timeout": "server.shutdown.timeout",
}

// ServeFlags adds the serve process mode's flags to fs.
func ServeFlags(fs *pflag.FlagSet) {
	d := server.Defaults()
	fs.String(ConfigFlag, "", "YAML configuration file")
	fs.String("listen", d.Listen, "API listener address (server.listen)")
	fs.String(
		"admin-listen",
		d.AdminListen,
		"admin listener address for the probes (server.admin_listen)",
	)
	fs.Duration("shutdown-timeout", d.Shutdown.Timeout,
		"how long in-flight requests may take to finish on shutdown (server.shutdown.timeout)")
}

// Load decodes the settings with flag > environment > file precedence. Viper
// ranks a changed flag above the environment and the file, and a flag left
// at its default below both, which is that order.
func Load(fs *pflag.FlagSet) (*Config, error) {
	v := viper.New()
	var bindErr error
	for name, key := range settingFlags {
		bindErr = errors.Join(bindErr, v.BindPFlag(key, fs.Lookup(name)))
	}
	if bindErr != nil {
		return nil, fmt.Errorf("bind flags: %w", bindErr)
	}
	v.SetEnvPrefix("INFRAWEAVE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	bindEnv(v, reflect.TypeFor[Config](), "")
	if file, _ := fs.GetString(ConfigFlag); file != "" {
		v.SetConfigFile(file)
		v.SetConfigType("yaml")
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}
	// Decoding over the defaults leaves every key no flag, variable or file
	// sets at its default; a flag's own default is the same value.
	c := Config{Server: server.Defaults()}
	if err := v.UnmarshalExact(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := c.Server.Validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &c, nil
}

// bindEnv binds the variable of every key of t, the mapstructure tags of its
// leaf fields. AutomaticEnv alone reads a variable only for a key viper
// already knows from a flag or the file, so a key with no flag would ignore
// its variable whenever the file left it out.
func bindEnv(v *viper.Viper, t reflect.Type, prefix string) {
	for i := range t.NumField() {
		f := t.Field(i)
		key := prefix + f.Tag.Get("mapstructure")
		if f.Type.Kind() == reflect.Struct {
			bindEnv(v, f.Type, key+".")
			continue
		}
		// BindEnv fails only when given no key.
		_ = v.BindEnv(key)
	}
}

// Version is the line `infraweave version` prints.
func Version() string {
	return fmt.Sprintf("infraweave %s (commit %s, built %s)",
		buildinfo.Version(), orUnknown(buildinfo.Commit()), orUnknown(buildinfo.BuildDate()))
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
