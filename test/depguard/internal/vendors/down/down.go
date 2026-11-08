// Package down breaks graph-vendors: the vendors domain imports the api/
// of stacks, which the declared graph builds after it, while the api/ of
// access, which it builds before, and kernel stay allowed.
package down

import (
	_ "github.com/weaveplatform/infraweave/internal/access/api"
	_ "github.com/weaveplatform/infraweave/internal/kernel"
	_ "github.com/weaveplatform/infraweave/internal/stacks/api"
)
