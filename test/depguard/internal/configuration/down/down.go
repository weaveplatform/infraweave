// Package down breaks graph-configuration: the configuration domain
// imports the api/ of runs, which the declared graph builds after it,
// while the api/ of features, access and stacks, which it builds before,
// and kernel stay allowed.
package down

import (
	_ "github.com/weaveplatform/infraweave/internal/access/api"
	_ "github.com/weaveplatform/infraweave/internal/features/api"
	_ "github.com/weaveplatform/infraweave/internal/kernel"
	_ "github.com/weaveplatform/infraweave/internal/runs/api"
	_ "github.com/weaveplatform/infraweave/internal/stacks/api"
)
