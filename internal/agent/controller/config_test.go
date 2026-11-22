package controller_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/weaveplatform/infraweave/internal/agent/controller"
)

const bootstrapImage = "ghcr.io/weaveplatform/infraweave@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func controllerFlags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	flags := pflag.NewFlagSet("worker-controller", pflag.ContinueOnError)
	controller.ConfigurationFlags(flags)
	flags.Bool("help", false, "help")
	if err := flags.Parse(args); err != nil {
		t.Fatal(err)
	}
	return flags
}

func controllerFile(t *testing.T, body string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "controller.yaml")
	if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func configuredSettings() controller.Settings {
	c := controller.DefaultSettings()
	c.ClusterID = "cluster"
	c.WatchNamespaces = []string{"workers"}
	c.LeaderElection.Namespace = "workers"
	c.BootstrapImage = bootstrapImage
	return c
}

func TestUnitControllerConfigurationPrecedenceAndSecretReferences(t *testing.T) {
	file := controllerFile(t, `worker_controller:
  cluster_id: file-cluster
  watch_namespaces: [file-workers]
  leader_election:
    namespace: leases
  bootstrap_image: `+bootstrapImage+`
  management_credentials_ref:
    name: management
    id_key: id
    secret_key: secret
  cleanup_timeout: 90s
`)
	t.Setenv("INFRAWEAVE_WORKER_CONTROLLER_CLUSTER_ID", "env-cluster")
	t.Setenv("INFRAWEAVE_WORKER_CONTROLLER_WATCH_NAMESPACES", "env-workers,second")
	c, err := controller.LoadSettings(controllerFlags(t, "--config", file, "--cluster-id", "flag-cluster", "--help"))
	if err != nil {
		t.Fatal(err)
	}
	if c.ClusterID != "flag-cluster" || len(c.WatchNamespaces) != 2 || c.WatchNamespaces[0] != "env-workers" || c.WatchNamespaces[1] != "second" ||
		c.CleanupTimeout != 90*time.Second ||
		c.ManagementCredentialsRef == nil ||
		c.ManagementCredentialsRef.Name != "management" ||
		c.LeaderElection.Name != "infraweave-worker-controller" ||
		!c.LeaderElection.Enabled {
		t.Fatalf("configuration precedence/references: %+v", c)
	}
	c, err = controller.LoadSettings(controllerFlags(t, "--config", file))
	if err != nil || c.ClusterID != "env-cluster" {
		t.Fatalf("environment precedence: %+v %v", c, err)
	}
}

func TestUnitControllerConfigurationReadsRequiredEnvironmentWithoutAFile(t *testing.T) {
	t.Setenv("INFRAWEAVE_WORKER_CONTROLLER_CLUSTER_ID", "cluster")
	t.Setenv("INFRAWEAVE_WORKER_CONTROLLER_WATCH_NAMESPACES", "workers")
	t.Setenv("INFRAWEAVE_WORKER_CONTROLLER_LEADER_ELECTION_NAMESPACE", "workers")
	t.Setenv("INFRAWEAVE_WORKER_CONTROLLER_BOOTSTRAP_IMAGE", bootstrapImage)
	c, err := controller.LoadSettings(controllerFlags(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.ManagementCredentialsRef != nil || c.InitializationTimeout != time.Minute || c.CleanupTimeout != 2*time.Minute || c.CacheDir != "/var/cache/infraweave" ||
		c.AdminListen != ":9091" {
		t.Fatalf("defaults or optional management identity: %+v", c)
	}
}

func TestUnitControllerConfigurationRejectsUnknownAndEmbeddedSecrets(t *testing.T) {
	for _, yaml := range []string{
		"worker_controller:\n  token: embedded\n",
		"worker_controller:\n  management_credentials_ref:\n    secret: embedded\n",
		"worker_controller:\n  leader_election:\n    force: true\n",
		"worker_controller:\n  pod_spec: {}\n",
		"server:\n  listen: :8080\n",
	} {
		if _, err := controller.LoadSettings(controllerFlags(t, "--config", controllerFile(t, yaml))); err == nil {
			t.Fatal("unknown or embedded credential configuration accepted")
		}
	}
	if _, err := controller.LoadSettings(controllerFlags(t, "--config", filepath.Join(t.TempDir(), "missing.yaml"))); err == nil {
		t.Fatal("missing configuration file accepted")
	}
}

func TestUnitControllerConfigurationRequiresBoundedScopeAndLeadership(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*controller.Settings)
	}{
		{"no namespaces", func(c *controller.Settings) { c.WatchNamespaces = nil }},
		{"duplicate namespace", func(c *controller.Settings) { c.WatchNamespaces = []string{"workers", "workers"} }},
		{"wildcard namespace", func(c *controller.Settings) { c.WatchNamespaces = []string{"*"} }},
		{"unstable cluster name", func(c *controller.Settings) { c.ClusterID = "Cluster" }},
		{"disabled leadership", func(c *controller.Settings) { c.LeaderElection.Enabled = false }},
		{"missing Lease scope", func(c *controller.Settings) { c.LeaderElection.Namespace = "" }},
		{"invalid Lease name", func(c *controller.Settings) { c.LeaderElection.Name = "../other" }},
		{"embedded credential as reference", func(c *controller.Settings) {
			c.ManagementCredentialsRef = &controller.CredentialReference{Name: "management", IDKey: "id", SecretKey: ""}
		}},
		{"one key used twice", func(c *controller.Settings) {
			c.ManagementCredentialsRef = &controller.CredentialReference{Name: "management", IDKey: "id", SecretKey: "id"}
		}},
		{"mutable image", func(c *controller.Settings) { c.BootstrapImage = "ghcr.io/weaveplatform/infraweave:latest" }},
		{"invalid image", func(c *controller.Settings) { c.BootstrapImage = "invalid image" }},
		{"relative cache", func(c *controller.Settings) { c.CacheDir = "relative" }},
		{"unbounded cleanup", func(c *controller.Settings) { c.CleanupTimeout = 0 }},
		{"invalid policy budget", func(c *controller.Settings) { c.InitializationTimeout = 61 * time.Second }},
		{"invalid admin address", func(c *controller.Settings) { c.AdminListen = "invalid" }},
		{"invalid admin port", func(c *controller.Settings) { c.AdminListen = ":70000" }},
		{"invalid log level", func(c *controller.Settings) { c.Log.Level = "all" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := configuredSettings()
			test.change(&c)
			if err := c.Validate(); !errors.Is(err, controller.ErrSettings) {
				t.Fatalf("invalid settings accepted: %v", err)
			}
		})
	}
	c := configuredSettings()
	c.ManagementCredentialsRef = &controller.CredentialReference{Name: "management", IDKey: "id", SecretKey: "secret"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.LoadSettings(controllerFlags(t)); err == nil || !strings.Contains(err.Error(), "watch_namespaces") {
		t.Fatalf("required configuration absent: %v", err)
	}
}
