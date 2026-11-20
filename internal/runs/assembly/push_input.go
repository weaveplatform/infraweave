package assembly

import runsapi "github.com/weaveplatform/infraweave/internal/runs/api"

// PushRunReader supplies account/stack-qualified facts without private runtime
// or workspace values; the caller owns the published policy projection.
func (d *Domain) PushRunReader() runsapi.PushRunReader { return d.store }
