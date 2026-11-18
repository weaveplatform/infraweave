// Package down breaks graph-events by importing configuration directly;
// VCS feedback, runs and kernel remain allowed by the declared graph.
package down

import (
	_ "github.com/weaveplatform/infraweave/internal/configuration/api"
	_ "github.com/weaveplatform/infraweave/internal/kernel"
	_ "github.com/weaveplatform/infraweave/internal/runs/api"
	_ "github.com/weaveplatform/infraweave/internal/vcs/api"
)
