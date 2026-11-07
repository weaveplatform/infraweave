package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	featuresassembly "github.com/weaveplatform/infraweave/internal/features/assembly"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/serve"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
)

// Config is every setting, keyed as in the YAML file.
type Config struct {
	Server   Server            `mapstructure:"server"`
	Database sql.Config        `mapstructure:"database"`
	Log      observe.LogConfig `mapstructure:"log"`
	Queue    queue.Config      `mapstructure:"queue"`
	Events   outbox.Config     `mapstructure:"events"`
	Secrets  crypt.Config      `mapstructure:"secrets"`
	// ObjectStore is decision 0005's object storage; Load checks its
	// settings, and the process modes require its driver.
	ObjectStore objectstore.Config `mapstructure:"objectstore"`
	// Auth is the access domain's: token lifetimes, the issuer and the
	// signing keys (decision 0006).
	Auth accessassembly.Settings `mapstructure:"auth"`
	// Features is the features domain's: the features switched off for
	// the whole installation (decision 0021).
	Features featuresassembly.Settings `mapstructure:"features"`
}

// Server holds the server settings: the listeners under server.listen,
// server.admin_listen and server.shutdown, decision 0003's controls under
// server.rate_limit and server.idempotency, and server.public_url.
type Server struct {
	serve.Config `mapstructure:",squash"`

	// PublicURL is decision 0020's server.public_url: the origin clients
	// reach infraweave at, from which every absolute URL it emits derives,
	// the token issuer among them. The modes that serve the API require it
	// (RequirePublicURL).
	PublicURL string `mapstructure:"public_url"`

	RateLimit   ratelimit.Config   `mapstructure:"rate_limit"`
	Idempotency idempotency.Config `mapstructure:"idempotency"`
}

// Defaults are the settings an empty configuration means.
func Defaults() Config {
	return Config{
		Server: Server{
			Config:      serve.Defaults(),
			RateLimit:   ratelimit.Defaults(),
			Idempotency: idempotency.Defaults(),
		},
		Database: sql.Defaults(),
		Log:      observe.LogDefaults(),
		Queue:    queue.Defaults(),
		Events:   outbox.Defaults(),
		Secrets:  crypt.Defaults(),

		ObjectStore: objectstore.Defaults(),
		Auth:        accessassembly.DefaultSettings(),
	}
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
	if s.PublicURL != "" {
		if u, err := url.Parse(
			s.PublicURL,
		); err != nil || (u.Scheme != "https" && u.Scheme != "http") ||
			u.Host == "" || (u.Path != "" && u.Path != "/") || u.User != nil ||
			u.RawQuery != "" ||
			u.Fragment != "" {
			errs = append(
				errs,
				fmt.Errorf("%w: server.public_url %q is not an http or https origin",
					serve.ErrConfig, s.PublicURL),
			)
		}
	}
	return errors.Join(errs...)
}

// RequirePublicURL fails when server.public_url is unset, which the modes
// that serve the API cannot run without.
func (s Server) RequirePublicURL() error {
	if s.PublicURL == "" {
		return fmt.Errorf("%w: server.public_url is required to serve the API", serve.ErrConfig)
	}
	return nil
}

// ConfigFlag names the YAML file. It is not a setting, so it is never bound.
const ConfigFlag = "config"

// settingFlags maps each setting flag to its configuration key. Only these
// are bound to viper: --config and --help are not settings, and
// UnmarshalExact would reject them as unknown keys. ServeFlags defines all
// of them; a command with fewer, such as migrate, binds those it has.
var settingFlags = map[string]string{
	"listen":           "server.listen",
	"admin-listen":     "server.admin_listen",
	"shutdown-timeout": "server.shutdown.timeout",
	"log-level":        "log.level",
}

// ServeFlags adds the flags of the process modes that serve the API to fs.
func ServeFlags(fs *pflag.FlagSet) {
	d := serve.Defaults()
	FileFlag(fs)
	fs.String("listen", d.Listen, "API listener address (server.listen)")
	fs.String(
		"admin-listen",
		d.AdminListen,
		"admin listener address for the probes (server.admin_listen)",
	)
	fs.Duration("shutdown-timeout", d.Shutdown.Timeout,
		"how long in-flight requests may take to finish on shutdown (server.shutdown.timeout)")
	LogFlags(fs)
}

// AdminFlags adds the flags of a process mode that serves only the admin
// listener, such as scheduler, to fs.
func AdminFlags(fs *pflag.FlagSet) {
	d := serve.Defaults()
	FileFlag(fs)
	fs.String(
		"admin-listen",
		d.AdminListen,
		"admin listener address for the probes (server.admin_listen)",
	)
	fs.Duration("shutdown-timeout", d.Shutdown.Timeout,
		"how long in-flight requests may take to finish on shutdown (server.shutdown.timeout)")
	LogFlags(fs)
}

// LogFlags adds --log-level to fs.
func LogFlags(fs *pflag.FlagSet) {
	fs.String("log-level", observe.LogDefaults().Level, "debug, info, warn or error (log.level)")
}

// FileFlag adds only --config, for a command that takes its settings from
// the file and the environment.
func FileFlag(fs *pflag.FlagSet) {
	fs.String(ConfigFlag, "", "YAML configuration file")
}

// Load decodes the settings with flag > environment > file precedence. Viper
// ranks a changed flag above the environment and the file, and a flag left
// at its default below both, which is that order.
func Load(fs *pflag.FlagSet) (*Config, error) {
	v := viper.New()
	for name, key := range settingFlags {
		if f := fs.Lookup(name); f != nil {
			// BindPFlag fails only on a nil flag.
			_ = v.BindPFlag(key, f)
		}
	}
	v.SetEnvPrefix("INFRAWEAVE")
	// A queue's name may hold a hyphen, as vcs-inbox does, which no
	// environment variable name can.
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	// Each key's variable is bound by name rather than through
	// AutomaticEnv, under which INFRAWEAVE_SECRETS_KEK, the local KEK,
	// would hide every key below secrets.kek.
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
	c.Secrets.KEK.Local.Key = os.Getenv(kek.Env)
	if err := errors.Join(
		c.Server.Validate(),
		c.Database.Validate(),
		c.Log.Validate(),
		c.Queue.Validate(),
		c.Events.Validate(),
		c.Secrets.Validate(),
		c.ObjectStore.Validate(),
		c.Auth.Validate(),
	); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &c, nil
}

// bindEnv binds the variable of every key of t, the mapstructure tags of its
// leaf fields, so a key no flag or file names still reads its variable. A
// squashed field's keys sit at its parent's level, and a field tagged "-"
// is no key.
func bindEnv(v *viper.Viper, t reflect.Type, prefix string) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("mapstructure")
		if tag == "-" {
			continue
		}
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

// LoadKEK decodes file, a YAML mapping of the keys under secrets.kek, as
// infraweave secrets rewrap --to reads the KEK it rewraps to. It decodes
// strictly over kek.Defaults, as Load does, and reads no flag or
// environment variable: the variables name the KEK the process runs with,
// not the one it moves to.
func LoadKEK(file string) (kek.Config, error) {
	v := viper.New()
	v.SetConfigFile(file)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return kek.Config{}, fmt.Errorf("read KEK settings: %w", err)
	}
	c := kek.Defaults()
	if err := v.UnmarshalExact(&c); err != nil {
		return kek.Config{}, fmt.Errorf("KEK settings: %w", err)
	}
	if err := c.Validate(); err != nil {
		return kek.Config{}, fmt.Errorf("KEK settings: %w", err)
	}
	return c, nil
}
