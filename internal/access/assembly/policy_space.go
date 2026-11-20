package assembly

import accessapi "github.com/weaveplatform/infraweave/internal/access/api"

// PolicySpaces supplies account-owned space labels and public identity.
func (d *Domain) PolicySpaces() accessapi.PolicySpaceReader { return d.store }
