package rego

import (
	"errors"
	"runtime/debug"
)

// ErrVersion means the binary does not expose the linked OPA version. A local
// module replacement must not be advertised as the pinned module it replaces.
var ErrVersion = errors.New("linked OPA module version unavailable")

// Version reads the library version linked into this binary, never a constant
// or an upstream service version.
func Version() (string, error) { info, ok := debug.ReadBuildInfo(); return linkedVersion(info, ok) }

func linkedVersion(info *debug.BuildInfo, ok bool) (string, error) {
	if !ok || info == nil {
		return "", ErrVersion
	}
	for _, dependency := range info.Deps {
		if dependency.Path != "github.com/open-policy-agent/opa" {
			continue
		}
		if dependency.Replace != nil {
			dependency = dependency.Replace
		}
		if dependency.Version == "" || dependency.Version == "(devel)" {
			return "", ErrVersion
		}
		return dependency.Version, nil
	}
	return "", ErrVersion
}
