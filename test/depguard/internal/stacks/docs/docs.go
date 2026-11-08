// Package docs breaks devtools: a domain imports the developer tooling,
// which graph-stacks refuses as well.
package docs

import _ "github.com/weaveplatform/infraweave/internal/devtools/specref"
