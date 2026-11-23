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

	"github.com/weaveplatform/infraweave/internal/agent/initpolicy"
	"github.com/weaveplatform/infraweave/internal/agent/phaseinputs"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/runnerimages"
)

// Config is the launcher's configuration, every key under launcher.
//
// ref: decision 0008 § "Configuration"
type Config struct {
	Launcher Launcher `mapstructure:"launcher"`
}

// Launcher holds the launcher.* settings.
type Launcher struct {
	// InitializationPolicy is an optional local Rego file compiled before registration.
	InitializationPolicy       string          `mapstructure:"initialization_policy"`
	InitializationPolicyEngine rego.EngineType `mapstructure:"initialization_policy_engine"`
	InitializationTimeout      time.Duration   `mapstructure:"initialization_timeout"`
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
	// none. When empty, the stack's vendor selects its built-in image.
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
	// RunTimeout is how long a run's container may run from its start
	// before the launcher kills it and the run fails.
	RunTimeout time.Duration `mapstructure:"run_timeout"`
	// LogTimeout is how long a run's container may go without a line on
	// its output before the launcher kills it and the run fails.
	LogTimeout time.Duration `mapstructure:"log_timeout"`
	// SensitiveOutputUpload lets a run keep its sensitive outputs' values
	// where its stack's enableSensitiveOutputUpload allows it too, as
	// SPACELIFT_SENSITIVE_OUTPUT_UPLOAD_ENABLED does; off, the runner
	// records each sensitive output with no value. The handoff carries it.
	SensitiveOutputUpload bool `mapstructure:"sensitive_output_upload"`
	// MaxRequestsPerSecond and MaxRequestsBurst are the API rate settings
	// exposed to clients in each run's environment.
	MaxRequestsPerSecond int `mapstructure:"max_requests_per_second"`
	MaxRequestsBurst     int `mapstructure:"max_requests_burst"`
	// MaskEnvs names the run's variables whose values the runner masks in
	// its log, whether or not they are secret, as SPACELIFT_MASK_ENVS
	// does; the handoff carries the names.
	MaskEnvs []string `mapstructure:"mask_envs"`
	// WhitelistEnvs names the launcher's variables each run receives, as
	// SPACELIFT_WHITELIST_ENVS does: a name prefixed ro_ reaches only the
	// read phases and wo_ only the write phases, under the name without
	// its prefix, and an unprefixed name every phase. A name the
	// launcher's environment does not set passes nothing.
	WhitelistEnvs []string `mapstructure:"whitelist_envs"`
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
	// ExtraMounts are host paths every phase of each run sees, each
	// <host path>:<container path>, optionally with :ro, which every one is:
	// no run writes to a shared host path. ROExtraMounts and WOExtraMounts
	// are host files the read phases or the write phases alone see: the
	// runner writes each at its container path when its phase begins and
	// removes it when a phase of the other side does.
	ExtraMounts   []string `mapstructure:"extra_mounts"`
	ROExtraMounts []string `mapstructure:"ro_extra_mounts"`
	WOExtraMounts []string `mapstructure:"wo_extra_mounts"`
}

// Mount is one parsed extra mount: the host path and the container path.
type Mount struct {
	Source string
	Target string
}

// errMount marks an extra mount that does not parse.
var errMount = errors.New("want <absolute host path>:<container path>, optionally with :ro")

// ParseMount parses an extra mount, <host path>:<container path>[:ro].
// The container path follows the last colon, so a host path may hold
// one, as a Windows drive does.
func ParseMount(s string) (Mount, error) {
	rest := strings.TrimSuffix(s, ":ro")
	i := strings.LastIndex(rest, ":")
	if i <= 0 {
		return Mount{}, errMount
	}
	m := Mount{Source: rest[:i], Target: rest[i+1:]}
	if !filepath.IsAbs(m.Source) || !strings.HasPrefix(m.Target, "/") {
		return Mount{}, errMount
	}
	return m, nil
}

// Mounts are d's extra mounts parsed: every phase's, the read phases'
// and the write phases'. Validate has checked that each parses.
func (d Docker) Mounts() (all, read, write []Mount) {
	parse := func(specs []string) []Mount {
		out := make([]Mount, 0, len(specs))
		for _, s := range specs {
			if m, err := ParseMount(s); err == nil {
				out = append(out, m)
			}
		}
		return out
	}
	return parse(d.ExtraMounts), parse(d.ROExtraMounts), parse(d.WOExtraMounts)
}

// validate checks every extra mount's form and their container paths
// together, as phaseinputs.CheckTargets does.
func (d Docker) validate() []error {
	var errs []error
	for _, set := range []struct {
		key   string
		specs []string
	}{{"extra_mounts", d.ExtraMounts}, {"ro_extra_mounts", d.ROExtraMounts}, {"wo_extra_mounts", d.WOExtraMounts}} {
		for _, s := range set.specs {
			if _, err := ParseMount(s); err != nil {
				errs = append(errs, fmt.Errorf("%w: launcher.docker.%s %q: %w", ErrConfig, set.key, s, err))
			}
		}
	}
	if len(errs) > 0 {
		return errs
	}
	all, read, write := d.Mounts()
	targets := func(ms ...[]Mount) []string {
		var out []string
		for _, list := range ms {
			for _, m := range list {
				out = append(out, m.Target)
			}
		}
		return out
	}
	if err := phaseinputs.CheckTargets(targets(all), targets(read, write)); err != nil {
		errs = append(errs, fmt.Errorf("%w: launcher.docker extra mounts: %w", ErrConfig, err))
	}
	return errs
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
		PollTimeout:                60 * time.Second,
		InitializationPolicyEngine: rego.V1,
		InitializationTimeout:      initpolicy.DefaultTimeout,
		CacheDir:                   filepath.Join(cache, "infraweave-launcher"),
		StateLockTimeout:           5 * time.Minute,
		RunTimeout:                 70 * time.Minute,
		LogTimeout:                 7 * time.Minute,
		// Match the server's standard rate class in platform/httpkit/
		// middleware/ratelimit; run clients share the server's defaults.
		MaxRequestsPerSecond: 10,
		MaxRequestsBurst:     100,
		AdminListen:          ":9091",
		Log:                  observe.LogDefaults(),
	}}
}

// VendorRunnerImage is the immutable environment used when neither the
// run specification nor launcher names an image. Terragrunt uses Terraform's
// environment even when it drives OpenTofu.
func VendorRunnerImage(vendor string) string {
	switch vendor {
	case "opentofu":
		return runnerimages.OpenTofu
	case "pulumi":
		return runnerimages.Pulumi
	case "ansible":
		return runnerimages.Ansible
	default:
		return runnerimages.Terraform
	}
}

// ErrConfig marks an invalid setting.
var ErrConfig = errors.New("invalid launcher configuration")

// Validate rejects a setting the launcher could not start with.
func (c Launcher) Validate() error {
	var errs []error
	if err := (initpolicy.Settings{Path: c.InitializationPolicy, Engine: c.InitializationPolicyEngine, Timeout: c.InitializationTimeout}).Validate(); err != nil {
		errs = append(errs, fmt.Errorf("%w: %w", ErrConfig, err))
	}
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
	if c.RunTimeout <= 0 {
		bad("launcher.run_timeout must be positive, got %s", c.RunTimeout)
	}
	if c.LogTimeout <= 0 {
		bad("launcher.log_timeout must be positive, got %s", c.LogTimeout)
	}
	if c.MaxRequestsPerSecond <= 0 {
		bad("launcher.max_requests_per_second must be positive, got %d", c.MaxRequestsPerSecond)
	}
	if c.MaxRequestsBurst <= 0 {
		bad("launcher.max_requests_burst must be positive, got %d", c.MaxRequestsBurst)
	}
	for _, name := range c.MaskEnvs {
		if !envName.MatchString(name) {
			bad("launcher.mask_envs %q: want a variable's name", name)
		}
	}
	errs = append(errs, c.validateInputs()...)
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

// validateInputs refuses a whitelisted variable or an extra mount the
// runner cannot expose by phase.
func (c Launcher) validateInputs() []error {
	var errs []error
	for _, name := range c.WhitelistEnvs {
		if _, _, err := phaseinputs.CheckVariable(name, ""); err != nil {
			errs = append(errs, fmt.Errorf("%w: launcher.whitelist_envs: %w", ErrConfig, err))
		}
	}
	if err := phaseinputs.CheckNames(c.WhitelistEnvs); err != nil {
		errs = append(errs, fmt.Errorf("%w: launcher.whitelist_envs: %w", ErrConfig, err))
	}
	return append(errs, c.Docker.validate()...)
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
	// Viper's weak decoding converts floats and booleans to integers.
	// Check the resolved values first so a fractional rate is not truncated.
	for _, key := range []string{"launcher.max_requests_per_second", "launcher.max_requests_burst"} {
		if v.IsSet(key) {
			if _, err := strconv.Atoi(v.GetString(key)); err != nil {
				return nil, fmt.Errorf("%w: %s must be an integer: %w", ErrConfig, key, err)
			}
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
