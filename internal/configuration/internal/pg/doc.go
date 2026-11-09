// Package pg is the configuration domain's PostgreSQL storage: Store, the
// contexts, their config elements and stacks' own, and the contexts'
// attachments to stacks, which seals each element's value to its row,
// never opens a write-only one, and locks one owner's elements for the
// writers of a stack, whose row is the stacks domain's;
// Stream, the migration stream that creates them; Held, the columns that
// name other domains' rows; and SealedColumns, the sealed value column.
//
// # Input and output
//
// PostgreSQL, through the pool its caller passes; the stream's migrations
// are embedded in the binary.
//
// # Dependencies
//
// It imports the domain's core package, internal/kernel and its search
// package, platform/crypt, platform/sql and its search, integrity and
// migrate packages, and platform/tx. The configuration domain's assembly constructs it and
// registers its stream.
package pg
