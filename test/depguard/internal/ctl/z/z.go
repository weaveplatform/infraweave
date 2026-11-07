// Package z breaks ctl twice: kernel is server code and pkg/client/api a
// generated client, while pkg/sdk and platform/buildinfo are allowed.
package z

import (
	_ "github.com/weaveplatform/infraweave/internal/kernel"
	_ "github.com/weaveplatform/infraweave/internal/platform/buildinfo"
	_ "github.com/weaveplatform/infraweave/pkg/client/api"
	_ "github.com/weaveplatform/infraweave/pkg/sdk"
)
