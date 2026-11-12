// Package api breaks the time.Sleep ban, which covers every file under
// test/, and acceptance-areas, by importing another area's steps. It
// passes api-packages, which no package of the acceptance suite is held
// to, with an import a domain's api package would be refused, and devtools,
// whose exemption covers the suite's steps, which check the committed
// documents with specref.
package api

import (
	"time"

	_ "github.com/weaveplatform/infraweave/internal/devtools/specref"
	_ "github.com/weaveplatform/infraweave/internal/platform/outboxutil"
	_ "github.com/weaveplatform/infraweave/test/acceptance/internal/steps/auth"
)

// Wait breaks the time.Sleep ban outside a _test.go file.
func Wait() {
	time.Sleep(time.Millisecond)
}
