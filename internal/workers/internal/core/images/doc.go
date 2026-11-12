// Package images keeps an account's default runner images: Service reads
// the image an account's runs on private pools use when neither their
// runtime configuration nor their stack names one, DefaultPrivate when
// the account sets none, and replaces both of an account's images, each
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
