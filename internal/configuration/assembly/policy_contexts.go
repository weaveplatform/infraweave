package assembly

import configapi "github.com/weaveplatform/infraweave/internal/configuration/api"

// PolicyContexts supplies effective attachment IDs without opening values.
func (d *Domain) PolicyContexts() configapi.PolicyContextReader { return d.svc }
