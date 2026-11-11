package assembly

import accessapi "github.com/weaveplatform/infraweave/internal/access/api"

// Tokens mints and verifies the worker protocol's tokens.
func (d *Domain) Tokens() accessapi.Tokens { return d.authn }

// BindRuns makes runs the reader of an in-run API token's run. Until it
// is bound, every in-run API token is refused.
func (d *Domain) BindRuns(runs accessapi.Runs) { d.authn.BindRuns(runs) }
