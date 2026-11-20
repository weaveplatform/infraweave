package assembly

import accessapi "github.com/weaveplatform/infraweave/internal/access/api"

// StackRoles supplies current role descriptions for account-owned stack facts.
func (d *Domain) StackRoles() accessapi.StackRoleReader { return d.store }
