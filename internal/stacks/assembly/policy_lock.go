package assembly

import stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"

// PolicyLocks supplies transactional changes to push-policy owner locks.
func (d *Domain) PolicyLocks() stacksapi.PolicyLocks { return d.stacks }
