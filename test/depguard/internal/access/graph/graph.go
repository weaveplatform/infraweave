// Package graph breaks graph-access: the access domain imports the api/ of
// stacks, which the declared graph builds after it, while kernel stays
// allowed.
package graph

import (
	_ "github.com/weaveplatform/infraweave/internal/kernel"
	_ "github.com/weaveplatform/infraweave/internal/stacks/api"
)
