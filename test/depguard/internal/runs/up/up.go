// Package up breaks graph-runs: the runs domain imports the api/ of vcs,
// which the declared graph builds after runs, while the api/ of stacks and
// configuration, which its row names, and kernel stay allowed.
package up

import (
	_ "github.com/weaveplatform/infraweave/internal/configuration/api"
	_ "github.com/weaveplatform/infraweave/internal/kernel"
	_ "github.com/weaveplatform/infraweave/internal/stacks/api"
	_ "github.com/weaveplatform/infraweave/internal/vcs/api"
)
