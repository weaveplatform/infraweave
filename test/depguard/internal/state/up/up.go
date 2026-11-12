// Package up breaks graph-state: the state domain imports the api/ of vcs,
// which the declared graph builds after state, while the api/ of stacks
// and runs, which its row names, and kernel stay allowed.
package up

import (
	_ "github.com/weaveplatform/infraweave/internal/kernel"
	_ "github.com/weaveplatform/infraweave/internal/runs/api"
	_ "github.com/weaveplatform/infraweave/internal/stacks/api"
	_ "github.com/weaveplatform/infraweave/internal/vcs/api"
)
