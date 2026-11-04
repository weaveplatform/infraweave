// Package leak breaks the compiler's internal rule: one domain imports
// another's internal tree.
package leak

import _ "github.com/weaveplatform/infraweave/internal/runs/internal/adapters"
