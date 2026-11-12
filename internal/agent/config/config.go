package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/weaveplatform/infraweave/internal/platform/observe"
)

// Config is the launcher's configuration, every key under launcher.
//
// ref: decision 0008 § "Configuration"
type Config struct {
	Launcher Launcher `mapstructure:"launcher"`
}

// Launcher holds the launcher.* settings.
type Launcher struct {
	// Token is the pool token, base64 of the JSON workerPoolCreate
	// answers.
	Token string `mapstructure:"token"`
	// PrivateKey is the pool's private key: a path to its PEM, or the PEM
	// itself, or base64 of the PEM, as Spacelift's launcher takes it.
	PrivateKey observe.Secret `mapstructure:"private_key"`
	// PollTimeout is how long a poll may be held when the server has
	// nothing to say, in whole seconds.
	PollTimeout time.Duration `mapstructure:"poll_timeout"`
	// DefaultRunnerImage is the image of a run whose specification names
	// none.
	DefaultRunnerImage string `mapstructure:"default_runner_image"`
	// CacheDir holds what the launcher keeps between starts: the worker it
	// registered, so a restart resumes it.
	CacheDir string `mapstructure:"cache_dir"`
	// Metadata describes the worker at registration. Each
	// INFRAWEAVE_LAUNCHER_METADATA_<key> variable adds <key>, as
	// SPACELIFT_METADATA_<key> does.
	Metadata map[string]string `mapstructure:"metadata"`
	// AdminListen is the admin listener's address, which serves /livez,
	// /startupz and /readyz.
	AdminListen string            `mapstructure:"admin_listen"`
	Log         observe.LogConfig `mapstructure:"log"`
	Docker      Docker            `mapstructure:"docker"`
}

// Docker holds the launcher.docker.* settings of the run containers.
type Docker struct {
	// Network is the network each run container joins; Docker's default
	// bridge when empty.
	Network string `mapstructure:"network"`
	// Runtime is the OCI runtime of each run container, such as runsc for
	// gVisor; Docker's default when empty.
	Runtime string `mapstructure:"runtime"`
}

// The poll's hold is between MinPoll and MaxPoll, the bounds the worker
// API's PollRequest.wait sets.
const (
	MinPoll = 5 * time.Second
	MaxPoll = 120 * time.Second
)

// The registration's metadata bounds, which match the worker API's: at
// most MaxMetadata entries, keys of MaxMetadataKey characters and values
// of MaxMetadataValue.
const (
	MaxMetadata      = 32
	MaxMetadataKey   = 64
	MaxMetadataValue = 255
)

// MetadataEnv prefixes each variable that adds a metadata entry.
const MetadataEnv = "INFRAWEAVE_LAUNCHER_METADATA_"

// Defaults are the settings an empty configuration means. The admin
// listener's port is one above the server's, so a launcher beside a
// server on one host starts with the defaults of both.
func Defaults() Config {
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	return Config{Launcher: Launcher{
		PollTimeout:        60 * time.Second,
		DefaultRunnerImage: "ghcr.io/weaveplatform/infraweave-runner-terraform",
		CacheDir:           filepath.Join(cache, "infraweave-launcher"),
		AdminListen:        ":9091",
		Log:                observe.LogDefaults(),
	}}
}

// ErrConfig marks an invalid setting.
var ErrConfig = errors.New("invalid launcher configuration")

// Validate rejects a setting the launcher could not start with.
func (c Launcher) Validate() error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s", ErrConfig, fmt.Sprintf(format, args...)))
	}
	if strings.TrimSpace(c.Token) == "" {
		bad("launcher.token is required")
	}
	if c.PrivateKey == "" {
		bad("launcher.private_key is required")
	}
	if c.PollTimeout < MinPoll || c.PollTimeout > MaxPoll || c.PollTimeout%time.Second != 0 {
		bad("launcher.poll_timeout must be whole seconds from %s to %s, got %s",
			MinPoll, MaxPoll, c.PollTimeout)
	}
	if c.DefaultRunnerImage == "" {
		bad("launcher.default_runner_image is required")
	}
	if c.CacheDir == "" {
		bad("launcher.cache_dir is required")
	}
	if len(c.Metadata) > MaxMetadata {
		bad("launcher.metadata holds at most %d entries, got %d", MaxMetadata, len(c.Metadata))
	}
	for k, v := range c.Metadata {
		if k == "" || len(k) > MaxMetadataKey || len(v) > MaxMetadataValue {
			bad("launcher.metadata %q: keys are 1 to %d characters and values at most %d",
				k, MaxMetadataKey, MaxMetadataValue)
		}
	}
	if _, port, err := net.SplitHostPort(c.AdminListen); err != nil {
		bad("launcher.admin_listen %q: want host:port: %v", c.AdminListen, err)
	} else if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		bad("launcher.admin_listen %q: port must be a number from 0 to 65535", c.AdminListen)
	}
	if err := c.Log.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("%w: launcher.log: %w", ErrConfig, err))
	}
	return errors.Join(errs...)
}

// Key reads the pool's private key PEM: PrivateKey itself when it is PEM
// or base64 of PEM, else the file it names.
func (c Launcher) Key() ([]byte, error) {
	v := strings.TrimSpace(c.PrivateKey.Reveal())
	if strings.HasPrefix(v, "-----BEGIN ") {
		return []byte(v), nil
	}
	if b, err := base64.StdEncoding.DecodeString(v); err == nil &&
		strings.HasPrefix(string(b), "-----BEGIN ") {
		return b, nil
	}
	b, err := os.ReadFile(v)
	if err != nil {
		return nil, fmt.Errorf("read launcher.private_key: %w", err)
	}
	return b, nil
}

// ConfigFlag names the configuration file.
const ConfigFlag = "config"

// settingFlags maps each setting flag to its key. --config and --help are
// not settings, and UnmarshalExact would reject them as unknown keys.
var settingFlags = map[string]string{
	"admin-listen": "launcher.admin_listen",
	"log-level":    "launcher.log.level",
}

// Flags adds the launcher's flags to fs.
func Flags(fs *pflag.FlagSet) {
	d := Defaults().Launcher
	fs.String(ConfigFlag, "", "YAML configuration file")
	fs.String("admin-listen", d.AdminListen, "admin listener address (launcher.admin_listen)")
	fs.String(
		"log-level",
		d.Log.Level,
		"log level: debug, info, warn or error (launcher.log.level)",
	)
}

// Load reads the configuration from fs's flags, the INFRAWEAVE_LAUNCHER_*
// environment and the file --config names, in that order of precedence,
// over the defaults. A key the configuration does not have fails.
func Load(fs *pflag.FlagSet) (*Config, error) {
	v := viper.New()
	for name, key := range settingFlags {
		if f := fs.Lookup(name); f != nil {
			// BindPFlag fails only on a nil flag.
			_ = v.BindPFlag(key, f)
		}
	}
	v.SetEnvPrefix("INFRAWEAVE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	bindEnv(v, reflect.TypeFor[Config](), "")
	if file, _ := fs.GetString(ConfigFlag); file != "" {
		v.SetConfigFile(file)
		v.SetConfigType("yaml")
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}
	c := Defaults()
	if err := v.UnmarshalExact(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	for _, kv := range os.Environ() {
		name, value, _ := strings.Cut(kv, "=")
		if key, ok := strings.CutPrefix(name, MetadataEnv); ok {
			if c.Launcher.Metadata == nil {
				c.Launcher.Metadata = map[string]string{}
			}
			c.Launcher.Metadata[key] = value
		}
	}
	if err := c.Launcher.Validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &c, nil
}

// bindEnv binds the variable of every leaf key of t, so a key no flag or
// file names still reads its variable. A map is one key, whose entries
// come from the file or from MetadataEnv's variables.
func bindEnv(v *viper.Viper, t reflect.Type, prefix string) {
	for f := range t.Fields() {
		key := prefix + f.Tag.Get("mapstructure")
		if f.Type.Kind() == reflect.Struct && f.Type != reflect.TypeFor[time.Duration]() {
			bindEnv(v, f.Type, key+".")
			continue
		}
		if f.Type.Kind() == reflect.Map {
			continue
		}
		// BindEnv fails only without a key.
		_ = v.BindEnv(key)
	}
}
