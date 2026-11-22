package runnerimages

import (
	_ "crypto/sha256" // Register OCI SHA-256 digest validation independently of callers.
	_ "crypto/sha512" // Register OCI SHA-384/SHA-512 digest validation.
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/distribution/reference"
)

// ValidRegistry accepts a registry authority or DNS suffix, plus the explicit
// ECR region selector. It accepts no URLs, paths or arbitrary wildcard syntax.
func ValidRegistry(registry string) bool {
	if registry == "dkr.ecr.<region>.amazonaws.com" {
		return true
	}
	if registry == "" || registry != strings.ToLower(registry) {
		return false
	}
	host := registry
	if strings.Contains(registry, ":") {
		var port string
		var err error
		host, port, err = net.SplitHostPort(registry)
		if err != nil {
			return false
		}
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return false
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Zone() == "" && !ip.Is4In6() && ip.String() == host
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}

// RegistryAllowed validates the complete OCI reference before comparing its
// normalized registry. Suffix matching uses DNS-label boundaries; a selector
// without a port admits only the default HTTPS port. Exact ports are opt-in.
func RegistryAllowed(image string, selectors []string) bool {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return false
	}
	domain := strings.ToLower(reference.Domain(named))
	host, port := registryAuthority(domain)
	for _, selector := range selectors {
		if !ValidRegistry(selector) {
			continue
		}
		if selector == "dkr.ecr.<region>.amazonaws.com" {
			parts := strings.Split(host, ".")
			if (port == "" || port == "443") && len(parts) == 6 && len(parts[0]) == 12 &&
				parts[1] == "dkr" && parts[2] == "ecr" && parts[4] == "amazonaws" && parts[5] == "com" {
				if _, err := strconv.ParseUint(parts[0], 10, 64); err == nil {
					return true
				}
			}
			continue
		}
		allowedHost, allowedPort := registryAuthority(selector)
		if allowedPort != "" && port != allowedPort || allowedPort == "" && port != "" && port != "443" {
			continue
		}
		// Artifact Registry regional hosts use LOCATION-docker.pkg.dev.
		if allowedHost == "docker.pkg.dev" && strings.HasSuffix(host, "-docker.pkg.dev") {
			location := strings.TrimSuffix(host, "-docker.pkg.dev")
			if !strings.Contains(location, ".") && ValidRegistry(location) {
				return true
			}
		}
		if host == allowedHost {
			return true
		}
		if _, err := netip.ParseAddr(allowedHost); err != nil && allowedHost != "localhost" && strings.HasSuffix(host, "."+allowedHost) {
			return true
		}
	}
	return false
}

func registryAuthority(value string) (string, string) {
	if host, port, err := net.SplitHostPort(value); err == nil {
		return host, port
	}
	return value, ""
}
