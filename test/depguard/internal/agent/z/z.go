// Package z breaks agent-isolation twice: platform/sql and pkg/sdk are outside
// the agent's allow list, while kernel, platform/observe and platform/mask are
// on it.
package z

import (
	_ "github.com/weaveplatform/infraweave/internal/kernel"
	_ "github.com/weaveplatform/infraweave/internal/platform/mask"
	_ "github.com/weaveplatform/infraweave/internal/platform/observe"
	_ "github.com/weaveplatform/infraweave/internal/platform/sql"
	_ "github.com/weaveplatform/infraweave/pkg/sdk"
)
