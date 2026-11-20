package assembly

import policyapi "github.com/weaveplatform/infraweave/internal/policies/api"

// Selection supplies a source-free hint before optional provider preparation.
func (d *Domain) Selection() policyapi.Selection { return d.svc }
