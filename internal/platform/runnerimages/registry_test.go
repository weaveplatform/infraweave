package runnerimages_test

import (
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/runnerimages"
)

func TestUnitRegistryAllowed(t *testing.T) {
	for _, tt := range []struct {
		name      string
		image     string
		selectors []string
		allowed   bool
	}{
		{"published digest", "ghcr.io/weaveplatform/runner@sha256:" + strings.Repeat("a", 64), []string{"ghcr.io"}, true},
		{"SHA-512 digest", "ghcr.io/weaveplatform/runner@sha512:" + strings.Repeat("a", 128), []string{"ghcr.io"}, true},
		{"Docker shorthand", "alpine:3.24", []string{"docker.io"}, true},
		{"Docker namespace", "library/alpine:3.24", []string{"docker.io"}, true},
		{"Docker legacy normalization", "index.docker.io/library/alpine:3.24", []string{"docker.io"}, true},
		{"default TLS port", "ghcr.io:443/weaveplatform/runner:1", []string{"ghcr.io"}, true},
		{"unlisted port", "ghcr.io:5000/weaveplatform/runner:1", []string{"ghcr.io"}, false},
		{"explicit port", "registry.example:5000/runner:1", []string{"registry.example:5000"}, true},
		{"different explicit port", "registry.example:5001/runner:1", []string{"registry.example:5000"}, false},
		{"Azure registry", "builders.azurecr.io/runner:1", []string{"azurecr.io"}, true},
		{"Google registry", "europe-west2-docker.pkg.dev/project/images/runner:1", []string{"docker.pkg.dev"}, true},
		{"Google empty location", "-docker.pkg.dev/project/images/runner:1", []string{"docker.pkg.dev"}, false},
		{"Google nested lookalike", "evil.europe-west2-docker.pkg.dev/project/images/runner:1", []string{"docker.pkg.dev"}, false},
		{"Google suffix lookalike", "europe-west2-docker.pkg.dev.evil.example/project/images/runner:1", []string{"docker.pkg.dev"}, false},
		{"ECR region", "123456789012.dkr.ecr.eu-west-1.amazonaws.com/runner:1", []string{"dkr.ecr.<region>.amazonaws.com"}, true},
		{"ECR wrong account", "123.dkr.ecr.eu-west-1.amazonaws.com/runner:1", []string{"dkr.ecr.<region>.amazonaws.com"}, false},
		{"ECR lookalike", "123456789012.dkr.ecr.eu-west-1.amazonaws.com.evil.example/runner:1", []string{"dkr.ecr.<region>.amazonaws.com"}, false},
		{"IP registry", "127.0.0.1:5000/runner:1", []string{"127.0.0.1:5000"}, true},
		{"IPv6 registry", "[::1]:5000/runner:1", []string{"[::1]:5000"}, true},
		{"IP lookalike", "evil.127.0.0.1:5000/runner:1", []string{"127.0.0.1:5000"}, false},
		{"local registry", "localhost:5000/runner:1", []string{"localhost:5000"}, true},
		{"localhost lookalike", "evil.localhost:5000/runner:1", []string{"localhost:5000"}, false},
		{"suffix lookalike", "evilghcr.io/runner:1", []string{"ghcr.io"}, false},
		{"prefix lookalike", "ghcr.io.evil.example/runner:1", []string{"ghcr.io"}, false},
		{"path lookalike", "evil.example/ghcr.io/runner:1", []string{"ghcr.io"}, false},
		{"empty policy", "ghcr.io/runner:1", nil, false},
		{"wildcard policy", "ghcr.io/runner:1", []string{"*"}, false},
		{"path policy", "ghcr.io/runner:1", []string{"ghcr.io/runner"}, false},
		{"URL", "https://ghcr.io/runner:1", []string{"ghcr.io"}, false},
		{"invalid digest", "ghcr.io/runner@sha256:bad", []string{"ghcr.io"}, false},
		{"invalid reference", "ghcr.io/runner name", []string{"ghcr.io"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := runnerimages.RegistryAllowed(tt.image, tt.selectors); got != tt.allowed {
				t.Fatalf("RegistryAllowed(%q,%q)=%t, want %t", tt.image, tt.selectors, got, tt.allowed)
			}
		})
	}
}
