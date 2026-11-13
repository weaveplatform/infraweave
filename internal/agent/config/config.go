package config

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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
	// registered, so a restart resumes it, and the tool binaries runs
	// mount, so each release is downloaded once.
	CacheDir string `mapstructure:"cache_dir"`
	// ToolMirror is the base URL of a mirror of the tool releases, for a
	// host that cannot reach the vendors: it serves each release's files
	// under <tool_mirror>/<tool>/<version>/, as releases.hashicorp.com
	// does, such as terraform/1.17.2/terraform_1.17.2_SHA256SUMS. Empty
	// downloads from the vendors.
	ToolMirror string `mapstructure:"tool_mirror"`
	// CAFile is a PEM file of certificates the launcher trusts beside the
	// system's roots, for a server, a Git provider, an archive store or a
	// tool mirror whose certificate a private CA issued.
	CAFile string `mapstructure:"ca_file"`
	// StateLockTimeout is how long each command of a run that locks the
	// state waits for the lock, which the handoff carries to the runner.
	StateLockTimeout time.Duration `mapstructure:"state_lock_timeout"`
	// MaskEnvs names the run's variables whose values the runner masks in
	// its log, whether or not they are secret, as SPACELIFT_MASK_ENVS
	// does; the handoff carries the names.
	MaskEnvs []string `mapstructure:"mask_envs"`
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
	// RunnerBinary is the infraweave executable mounted into each run
	// container as its runner: the launcher's own when empty. A launcher
	// whose own build cannot run in a Linux container, such as one on
	// macOS, names a statically linked Linux build of the same version.
	RunnerBinary string `mapstructure:"runner_binary"`
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

// envName is a variable's name, as a shell takes it.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
		StateLockTimeout:   5 * time.Minute,
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
	if u, err := url.Parse(c.ToolMirror); c.ToolMirror != "" &&
		(err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "") {
		bad("launcher.tool_mirror %q: want an http or https URL", c.ToolMirror)
	}
	if c.StateLockTimeout < 0 {
		bad("launcher.state_lock_timeout must not be negative, got %s", c.StateLockTimeout)
	}
	for _, name := range c.MaskEnvs {
		if !envName.MatchString(name) {
			bad("launcher.mask_envs %q: want a variable's name", name)
		}
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

// CA reads CAFile: its PEM, and the system's roots with its
// certificates added; none when CAFile is empty.
func (c Launcher) CA() ([]byte, *x509.CertPool, error) {
	if c.CAFile == "" {
		return nil, nil, nil
	}
	b, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, nil, fmt.Errorf("read launcher.ca_file: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, nil, fmt.Errorf("launcher.ca_file: the system's roots: %w", err)
	}
	if !roots.AppendCertsFromPEM(b) {
		return nil, nil, fmt.Errorf("%w: launcher.ca_file %s holds no PEM certificate",
			ErrConfig, c.CAFile)
	}
	return b, roots, nil
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
