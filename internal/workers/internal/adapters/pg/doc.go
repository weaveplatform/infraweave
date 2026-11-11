// Package pg is the workers domain's PostgreSQL storage: Store, the
// private worker pools, whose names are unique per account, searched by
// the pool package's Fields; the installation's one pool CA, created on
// first use with its key sealed to its row, whose next serial each
// signing takes under the CA row's lock; a pool's delete, which takes its
// workers and their nonces with it; and a deleted account's purge, a
// batch of its nonces, workers or pools at a time. Stream is the
// migration stream that creates them, Held the columns that name other
// domains' rows, and SealedColumns the CA's sealed private key.
//
// # Input and output
//
// PostgreSQL, through the pool its caller passes; the stream's migrations
// are embedded in the binary.
//
// # Dependencies
//
// It imports pgx, the domain's pool package, internal/kernel and its
// search package, platform/crypt, platform/sql and its search, integrity
// and migrate packages, and platform/tx. The domain's assembly constructs
// it and internal/app registers its stream.
package pg
