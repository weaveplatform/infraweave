// Package crypt is decision 0013's keyring: the data keys every sealed
// column and sealed object is sealed under, each wrapped by a KEK that
// never enters the database.
//
// Load unwraps every active and retiring key through a kek.Provider when
// a process mode starts. The first replica on an empty keyring creates
// key 1, and one that loses that race reads the winner's; a key that does
// not unwrap stops the start with an error naming its ID. Seal seals under
// the active key in platform/seal's column format, and Open opens under an
// active or retiring key. Store code seals through SealColumn and
// OpenColumn, which build the associated data <table>.<column>:<primary
// key> from a Column, so a sealed value copied to another row or column
// fails to open. Open returns a Value, which renders as [redacted] in fmt,
// log/slog, encoding/json and encoding/text; Reveal returns the bytes.
// DeriveKey returns HKDF-SHA256 of the active key with info
// infraweave/<purpose>, and the key's ID, which DerivedKey takes to verify
// what a retiring key made. SealObject and OpenObject stream an object in
// seal's object format under a fresh content key, which the caller stores
// sealed beside it. Registry lists the sealed columns of every stream.
//
// # Input and output
//
// Load reads and, on a first start, inserts encryption_keys, the platform
// stream's table, and calls the provider once per key. Database errors
// that mean PostgreSQL is unreachable are marked with
// kernel.ErrUnavailable. Sealing and opening touch neither the database
// nor the provider. Nothing is logged.
//
// # Dependencies
//
// It imports platform/seal for the formats, crypt/kek for the KEK,
// platform/sql for errors and pgx for the queries. internal/app loads the
// keyring before any listener binds; internal/agent never imports it.
package crypt
