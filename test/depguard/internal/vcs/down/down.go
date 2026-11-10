// Package down breaks graph-vcs: the vcs domain imports the api/ of
// configuration, which its row of the declared graph does not name, while
// the api/ of stacks and runs, which it names, and kernel stay allowed.
package down

import (
	_ "github.com/weaveplatform/infraweave/internal/configuration/api"
	_ "github.com/weaveplatform/infraweave/internal/kernel"
	_ "github.com/weaveplatform/infraweave/internal/runs/api"
	_ "github.com/weaveplatform/infraweave/internal/stacks/api"
)
