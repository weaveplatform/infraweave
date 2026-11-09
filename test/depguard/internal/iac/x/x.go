// Package x breaks iac-pure three times: a domain, platform/sql and pgx are
// outside iac's allow list, while kernel is on it. The domain import also
// breaks shared-below-domains.
package x

import (
	_ "github.com/jackc/pgx/v5"

	_ "github.com/weaveplatform/infraweave/internal/kernel"
	_ "github.com/weaveplatform/infraweave/internal/platform/sql"
	_ "github.com/weaveplatform/infraweave/internal/stacks"
)
