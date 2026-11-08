// Package wire breaks constructors: a domain imports another's assembly,
// which graph-stacks refuses as well.
package wire

import _ "github.com/weaveplatform/infraweave/internal/runs/assembly"
