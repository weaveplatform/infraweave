package controller

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/weaveplatform/infraweave/internal/agent/initpolicy"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

var ErrSettings = errors.New("invalid worker controller configuration")

type CredentialReference struct {
	Name      string `mapstructure:"name"`
	IDKey     string `mapstructure:"id_key"`
	SecretKey string `mapstructure:"secret_key"`
}

type LeaderElection struct {
	Enabled   bool   `mapstructure:"enabled"`
	Name      string `mapstructure:"name"`
	Namespace string `mapstructure:"namespace"`
}

// Settings contains only references and process configuration. Each watched
// namespace resolves its own named Secret; opened values never enter settings.
type Settings struct {
	WatchNamespaces          []string             `mapstructure:"watch_namespaces"`
	ClusterID                string               `mapstructure:"cluster_id"`
	ManagementCredentialsRef *CredentialReference `mapstructure:"management_credentials_ref"`
	LeaderElection           LeaderElection       `mapstructure:"leader_election"`
	BootstrapImage           string               `mapstructure:"bootstrap_image"`
	CacheDir                 string               `mapstructure:"cache_dir"`
	ToolMirror               string               `mapstructure:"tool_mirror"`
	CAFile                   string               `mapstructure:"ca_file"`
	AdminListen              string               `mapstructure:"admin_listen"`
	InitializationPolicy     string               `mapstructure:"initialization_policy"`
	InitializationTimeout    time.Duration        `mapstructure:"initialization_timeout"`
	CleanupTimeout           time.Duration        `mapstructure:"cleanup_timeout"`
	Log                      observe.LogConfig    `mapstructure:"log"`
}

func DefaultSettings() Settings {
	return Settings{
		LeaderElection:        LeaderElection{Enabled: true, Name: "infraweave-worker-controller"},
		CacheDir:              "/var/cache/infraweave",
		AdminListen:           ":9091",
		InitializationTimeout: initpolicy.DefaultTimeout,
		CleanupTimeout:        120 * time.Second,
		Log:                   observe.LogDefaults(),
	}
}

func (s Settings) Validate() error {
	var faults []error
	bad := func(message string) { faults = append(faults, fmt.Errorf("%w: %s", ErrSettings, message)) }
	if len(s.WatchNamespaces) == 0 {
		bad("worker_controller.watch_namespaces is required")
	}
	seen := map[string]bool{}
	for _, namespace := range s.WatchNamespaces {
		if len(validation.IsDNS1123Label(namespace)) > 0 || seen[namespace] {
			bad("worker_controller.watch_namespaces must contain unique namespace DNS labels")
		}
		seen[namespace] = true
	}
	if len(validation.IsDNS1123Label(s.ClusterID)) > 0 {
		bad("worker_controller.cluster_id must be a stable 1..63-character DNS label")
	}
	if !s.LeaderElection.Enabled || len(validation.IsDNS1123Subdomain(s.LeaderElection.Name)) > 0 || len(validation.IsDNS1123Label(s.LeaderElection.Namespace)) > 0 {
		bad("worker_controller.leader_election requires enabled election, a Lease name and an explicit namespace")
	}
	if ref := s.ManagementCredentialsRef; ref != nil {
		if len(validation.IsDNS1123Subdomain(ref.Name)) > 0 || ref.IDKey == "" || ref.SecretKey == "" ||
			len(validation.IsConfigMapKey(ref.IDKey)) > 0 || len(validation.IsConfigMapKey(ref.SecretKey)) > 0 || ref.IDKey == ref.SecretKey {
			bad("worker_controller.management_credentials_ref requires a Secret name and distinct valid id_key/secret_key references")
		}
	}
	image, err := reference.ParseNormalizedNamed(s.BootstrapImage)
	if err != nil {
		bad("worker_controller.bootstrap_image must be an immutable OCI image digest")
	} else if _, immutable := image.(reference.Digested); !immutable {
		bad("worker_controller.bootstrap_image must be an immutable OCI image digest")
	}
	if !path.IsAbs(s.CacheDir) && !filepath.IsAbs(s.CacheDir) {
		bad("worker_controller.cache_dir must be an absolute ephemeral path")
	}
	if !mirror(s.ToolMirror) {
		bad("worker_controller.tool_mirror must be an absolute http or https URL")
	}
	if s.CleanupTimeout <= 0 {
		bad("worker_controller.cleanup_timeout must be positive")
	}
	if err := (initpolicy.Settings{Path: s.InitializationPolicy, Engine: rego.V1, Timeout: s.InitializationTimeout}).Validate(); err != nil {
		faults = append(faults, fmt.Errorf("%w: %w", ErrSettings, err))
	}
	_, port, err := net.SplitHostPort(s.AdminListen)
	if err != nil {
		bad("worker_controller.admin_listen must be host:port")
	} else if number, err := strconv.Atoi(port); err != nil || number < 0 || number > 65535 {
		bad("worker_controller.admin_listen port must be in 0..65535")
	}
	if err := s.Log.Validate(); err != nil {
		faults = append(faults, fmt.Errorf("%w: worker_controller.log: %w", ErrSettings, err))
	}
	return errors.Join(faults...)
}

// mirror reports whether a tool mirror is unset or an absolute HTTP or
// HTTPS URL.
func mirror(v string) bool {
	if v == "" {
		return true
	}
	u, err := url.Parse(v)
	return err == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http")
}

// ConfigurationFlags binds only settings; --config and Cobra's --help are not
// configuration fields and must not participate in UnmarshalExact.
func ConfigurationFlags(flags *pflag.FlagSet) {
	d := DefaultSettings()
	flags.String("config", "", "YAML configuration file")
	flags.String("cluster-id", "", "stable worker controller cluster identity")
	flags.StringSlice("watch-namespaces", nil, "explicit namespaces to watch")
	flags.String("admin-listen", d.AdminListen, "controller admin listener address")
	flags.String("log-level", d.Log.Level, "controller log level")
}

func LoadSettings(flags *pflag.FlagSet) (Settings, error) {
	v := viper.New()
	for flag, key := range map[string]string{
		"cluster-id": "cluster_id", "watch-namespaces": "watch_namespaces", "admin-listen": "admin_listen", "log-level": "log.level",
	} {
		if binding := flags.Lookup(flag); binding != nil {
			_ = v.BindPFlag("worker_controller."+key, binding)
		}
	}
	v.SetEnvPrefix("INFRAWEAVE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	for _, key := range []string{
		"watch_namespaces", "cluster_id", "management_credentials_ref.name", "management_credentials_ref.id_key", "management_credentials_ref.secret_key",
		"leader_election.enabled", "leader_election.name", "leader_election.namespace", "bootstrap_image", "cache_dir", "tool_mirror", "ca_file", "admin_listen",
		"initialization_policy", "initialization_timeout", "cleanup_timeout", "log.level", "log.format",
	} {
		_ = v.BindEnv("worker_controller." + key)
	}
	if file, _ := flags.GetString("config"); file != "" {
		v.SetConfigFile(file)
		v.SetConfigType("yaml")
		if err := v.ReadInConfig(); err != nil {
			return Settings{}, fmt.Errorf("worker controller: read configuration: %w", err)
		}
	}
	c := struct {
		Controller Settings `mapstructure:"worker_controller"`
	}{Controller: DefaultSettings()}
	if err := v.UnmarshalExact(&c); err != nil {
		return Settings{}, fmt.Errorf("worker controller: decode configuration: %w", err)
	}
	if err := c.Controller.Validate(); err != nil {
		return Settings{}, err
	}
	return c.Controller, nil
}
