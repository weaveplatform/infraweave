package api

import "context"

// Vendor names a vendor the catalogue lists, as internal/iac's Kind names
// it.
type Vendor string

// The vendors the catalogue lists.
const (
	Terraform Vendor = "terraform"
	OpenTofu  Vendor = "opentofu"
)

// Catalog is the version catalogue: the stable releases each vendor's
// release index lists, as the server last refreshed them.
type Catalog interface {
	// Versions are vendor's listed releases, newest first.
	Versions(ctx context.Context, vendor Vendor) ([]string, error)
	// Effective is the newest listed release of vendor that constraints
	// allow. Constraints that do not parse, or that no listed release
	// meets, are a *kernel.Invalid naming the constraints parameter.
	Effective(ctx context.Context, vendor Vendor, constraints string) (string, error)
}
