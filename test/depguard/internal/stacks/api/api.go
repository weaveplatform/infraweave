// Package api breaks api-packages through the prefix trap: platform/outboxutil
// would pass if the outbox entry lost its trailing $. kernel is allowed.
package api

import (
	_ "fmt"

	_ "github.com/weaveplatform/infraweave/internal/kernel"
	_ "github.com/weaveplatform/infraweave/internal/platform/outboxutil"
)
