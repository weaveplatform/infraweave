// Package up breaks graph-features: the features domain imports access'
// api/, though it sits below every domain, while kernel stays allowed.
package up

import (
	_ "github.com/weaveplatform/infraweave/internal/access/api"
	_ "github.com/weaveplatform/infraweave/internal/kernel"
)
