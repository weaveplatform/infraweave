package config

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/serve"
)

// Config is every setting, keyed as in the YAML file.
type Config struct {
	Server Server `mapstructure:"server"`
}

// Server holds the server settings: the listeners under server.listen,
// server.admin_listen and server.shutdown, and decision 0003's controls under
// server.rate_limit and server.idempotency.
type Server struct {
	serve.Config `mapstructure:",squash"`

	RateLimit   ratelimit.Config   `mapstructure:"rate_limit"`
	Idempotency idempotency.Config `mapstructure:"idempotency"`
}

// Defaults are the settings an empty configuration means.
func Defaults() Config {
	return Config{Server: Server{
		Config:      serve.Defaults(),
		RateLimit:   ratelimit.Defaults(),
		Idempotency: idempotency.Defaults(),
	}}
}

// Validate rejects a setting the server could not start with, so a typo fails
// before anything listens rather than as a bind error on one listener.
func (s Server) Validate() error {
	errs := []error{s.Config.Validate()}
	if err := s.RateLimit.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("%w: server.rate_limit.%w", serve.ErrConfig, err))
	}
	if err := s.Idempotency.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("%w: server.idempotency.%w", serve.ErrConfig, err))
	}
	return errors.Join(errs...)
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
	d := serve.Defaults()
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
	c := Defaults()
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
// its variable whenever the file left it out. A squashed field's keys sit at
// its parent's level.
func bindEnv(v *viper.Viper, t reflect.Type, prefix string) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("mapstructure")
		if tag == ",squash" {
			bindEnv(v, f.Type, prefix)
			continue
		}
		key := prefix + tag
		if f.Type.Kind() == reflect.Struct {
			bindEnv(v, f.Type, key+".")
			continue
		}
		// BindEnv fails only when given no key.
		_ = v.BindEnv(key)
	}
}
