package assembly

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/runnerimages"
)

func TestUnitPublicPoolSettings(t *testing.T) {
	defaults := DefaultSettings()
	if defaults.PublicPool.Enabled || defaults.PublicPool.PreparingTimeout != 15*time.Minute || len(defaults.PublicPool.AllowedRegistries) != 10 {
		t.Fatalf("public pool defaults: %+v", defaults.PublicPool)
	}
	if err := defaults.Validate(); err != nil {
		t.Fatal(err)
	}
	other := DefaultSettings()
	other.PublicPool.AllowedRegistries[0] = "changed.example"
	if reflect.DeepEqual(defaults.PublicPool.AllowedRegistries, other.PublicPool.AllowedRegistries) {
		t.Fatal("defaults share their mutable registry allowlist")
	}
	for _, tt := range []struct {
		name       string
		ips        []string
		registries []string
		timeout    time.Duration
		valid      bool
	}{
		{name: "configured", ips: []string{"192.0.2.1", "2001:db8::1"}, registries: []string{"ghcr.io"}, timeout: time.Minute, valid: true},
		{name: "deny all images", timeout: time.Second, valid: true},
		{name: "custom registries", registries: []string{"registry.example:5000", "127.0.0.1:5000", "[::1]:5000"}, timeout: time.Second, valid: true},
		{name: "zero timeout"},
		{name: "negative timeout", timeout: -time.Second},
		{name: "fractional timeout", timeout: time.Second + time.Nanosecond},
		{name: "IP hostname", ips: []string{"example.com"}, timeout: time.Second},
		{name: "IP port", ips: []string{"192.0.2.1:443"}, timeout: time.Second},
		{name: "IP zone", ips: []string{"fe80::1%eth0"}, timeout: time.Second},
		{name: "mapped IPv4", ips: []string{"::ffff:192.0.2.1"}, timeout: time.Second},
		{name: "noncanonical IPv6", ips: []string{"2001:0db8::1"}, timeout: time.Second},
		{name: "duplicate IP", ips: []string{"192.0.2.1", "192.0.2.1"}, timeout: time.Second},
		{name: "empty registry", registries: []string{""}, timeout: time.Second},
		{name: "registry URL", registries: []string{"https://ghcr.io"}, timeout: time.Second},
		{name: "registry path", registries: []string{"ghcr.io/project"}, timeout: time.Second},
		{name: "registry case", registries: []string{"GHCR.IO"}, timeout: time.Second},
		{name: "registry space", registries: []string{" ghcr.io"}, timeout: time.Second},
		{name: "registry wildcard", registries: []string{"*"}, timeout: time.Second},
		{name: "registry empty label", registries: []string{"ghcr..io"}, timeout: time.Second},
		{name: "registry dash label", registries: []string{"-ghcr.io"}, timeout: time.Second},
		{name: "registry long label", registries: []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.io"}, timeout: time.Second},
		{name: "registry port zero", registries: []string{"ghcr.io:0"}, timeout: time.Second},
		{name: "registry port overflow", registries: []string{"ghcr.io:65536"}, timeout: time.Second},
		{name: "registry port absent", registries: []string{"ghcr.io:"}, timeout: time.Second},
		{name: "duplicate registry", registries: []string{"ghcr.io", "ghcr.io"}, timeout: time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := PublicPoolSettings{OutgoingIPAddresses: tt.ips, AllowedRegistries: tt.registries, PreparingTimeout: tt.timeout}
			err := s.Validate()
			if tt.valid && err != nil || !tt.valid && !errors.Is(err, ErrConfig) {
				t.Fatalf("Validate() = %v, valid=%v", err, tt.valid)
			}
		})
	}
}

// A launcher with no default of its own runs a vendor's shipped GHCR image,
// so the installation's default allowlist must admit every shipped image
// and a narrowed one must refuse them.
func TestUnitPublicPoolDefaultsAdmitShippedRunnerImages(t *testing.T) {
	registries := DefaultSettings().PublicPool.AllowedRegistries
	for _, image := range []string{
		runnerimages.Terraform, runnerimages.OpenTofu, runnerimages.Pulumi, runnerimages.Ansible, runnerimages.TestSupport,
	} {
		if !runnerimages.RegistryAllowed(image, registries) {
			t.Errorf("default public allowlist refuses the shipped image %s", image)
		}
		if runnerimages.RegistryAllowed(image, []string{"quay.io"}) {
			t.Errorf("a narrowed public allowlist admits the shipped image %s", image)
		}
	}
}
