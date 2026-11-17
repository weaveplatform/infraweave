// Package images keeps an account's default runner images: Service reads
// the image an account's runs on private pools use when neither their
// runtime configuration nor their stack names one, DefaultPrivate when
// the account sets none. Runs without an account default use the launcher's
// configured default, else their vendor's image: OpenTofu's for OpenTofu,
// Terraform's for Terraform and Terragrunt. Service replaces both images, each
// trimmed, refusing one too long or holding a space.
//
// ref: decision 0008 § "Runner images"
//
// # Input and output
//
// New takes the Store.
//
// # Dependencies
//
// It imports internal/kernel. The domain's assembly constructs it over
// the pg adapter.
package images
