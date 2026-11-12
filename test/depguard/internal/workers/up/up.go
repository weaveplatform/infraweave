// Package up breaks graph-workers: the workers domain imports the api/ of
// stacks, built after it, while the api/ of features and access, which
// its row of the declared graph names, and kernel stay allowed.
package up

import (
	_ "github.com/weaveplatform/infraweave/internal/access/api"
	_ "github.com/weaveplatform/infraweave/internal/features/api"
	_ "github.com/weaveplatform/infraweave/internal/kernel"
	_ "github.com/weaveplatform/infraweave/internal/stacks/api"
)
