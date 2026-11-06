package app

import (
	"crypto/fips140"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/platform/buildinfo"
)

// Version is the line `infraweave version` prints.
func Version() string {
	return fmt.Sprintf("infraweave %s (commit %s, built %s)",
		buildinfo.Version(), orUnknown(buildinfo.Commit()), orUnknown(buildinfo.BuildDate()))
}

// FIPS is the line `infraweave version` prints after Version (decision
// 0013): FIPS 140-3 mode off, on, or only, as GODEBUG=fips140=only
// enforces it, and the Go Cryptographic Module's version, which is
// "latest" unless the binary was built with GOFIPS140 naming a frozen
// module, such as certified.
func FIPS() string {
	return fipsLine(fips140.Enabled(), fips140.Enforced(), fips140.Version())
}

func fipsLine(enabled, enforced bool, module string) string {
	mode := "off"
	switch {
	case enforced:
		mode = "only"
	case enabled:
		mode = "on"
	}
	return fmt.Sprintf("FIPS 140-3 mode %s, Go Cryptographic Module %s", mode, module)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
