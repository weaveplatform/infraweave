package assembly

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/runnerimages"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// PublicPoolSettings configure installation-owned execution independently of
// account-owned private pools. Enabling the pool does not create worker hosts.
type PublicPoolSettings struct {
	Enabled             bool          `mapstructure:"enabled"`
	AllowedRegistries   []string      `mapstructure:"allowed_registries"`
	OutgoingIPAddresses []string      `mapstructure:"outgoing_ip_addresses"`
	PreparingTimeout    time.Duration `mapstructure:"preparing_timeout"`
}

func defaultPublicPoolSettings() PublicPoolSettings {
	return PublicPoolSettings{
		AllowedRegistries: []string{
			"azurecr.io", "dkr.ecr.<region>.amazonaws.com", "docker.io",
			"docker.pkg.dev", "gcr.io", "ghcr.io", "public.ecr.aws",
			"quay.io", "registry.gitlab.com", "registry.hub.docker.com",
		},
		OutgoingIPAddresses: []string{},
		PreparingTimeout:    pool.DefaultPreparingTimeout,
	}
}

// Validate rejects ambiguous egress values rather than publishing addresses
// different from the operator's assertion. An empty allowlist admits no images.
func (s PublicPoolSettings) Validate() error {
	if s.PreparingTimeout <= 0 || s.PreparingTimeout%time.Second != 0 {
		return fmt.Errorf("%w: worker.public_pool.preparing_timeout must be positive whole seconds", ErrConfig)
	}
	seen := make(map[string]bool, len(s.OutgoingIPAddresses))
	for _, value := range s.OutgoingIPAddresses {
		ip, err := netip.ParseAddr(value)
		if err != nil || ip.Zone() != "" || ip.Is4In6() || ip.String() != value {
			return fmt.Errorf("%w: worker.public_pool.outgoing_ip_addresses contains a noncanonical IP literal %q", ErrConfig, value)
		}
		if seen[value] {
			return fmt.Errorf("%w: worker.public_pool.outgoing_ip_addresses repeats %q", ErrConfig, value)
		}
		seen[value] = true
	}
	seen = make(map[string]bool, len(s.AllowedRegistries))
	for _, registry := range s.AllowedRegistries {
		if !runnerimages.ValidRegistry(registry) {
			return fmt.Errorf("%w: worker.public_pool.allowed_registries contains an invalid registry %q", ErrConfig, registry)
		}
		if seen[registry] {
			return fmt.Errorf("%w: worker.public_pool.allowed_registries repeats %q", ErrConfig, registry)
		}
		seen[registry] = true
	}
	return nil
}
