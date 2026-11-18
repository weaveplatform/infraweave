package assembly

import runsapi "github.com/weaveplatform/infraweave/internal/runs/api"

// Creator creates runs inside a domain caller's transaction, with an explicit
// deduplication rule and an optional transactional admission check.
func (d *Domain) Creator() runsapi.Creator { return d.trigger }

// Supersede requests worker stops for a branch's older planning proposals,
// inside the caller's transaction.
func (d *Domain) Supersede() runsapi.Supersede { return d.control }
