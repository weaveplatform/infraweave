// Package up breaks graph-expressions: the expressions domain imports the
// api/ of stacks, though it imports no domain, while kernel stays allowed.
package up

import (
	_ "github.com/weaveplatform/infraweave/internal/kernel"
	_ "github.com/weaveplatform/infraweave/internal/stacks/api"
)
