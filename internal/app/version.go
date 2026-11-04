package app

import (
	"fmt"

	"github.com/weaveplatform/infraweave/internal/platform/buildinfo"
)

// Version is the line `infraweave version` prints.
func Version() string {
	return fmt.Sprintf("infraweave %s (commit %s, built %s)",
		buildinfo.Version(), orUnknown(buildinfo.Commit()), orUnknown(buildinfo.BuildDate()))
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
