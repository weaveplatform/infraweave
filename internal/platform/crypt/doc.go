// Package crypt is the server's keyring: the data keys every sealed column
// and sealed object is sealed under, each wrapped by a KEK that never enters
// the database.
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
// sealed beside it, with its Column's AAD as associated data. Registry lists
// the sealed columns of every stream, blob_objects.content_key_sealed among
// them.
//
// Rotate makes a new active key and marks the old one retiring, in the
// caller's transaction, and refuses with ErrRotating while a retiring key
// remains. Reseal walks Registry and reseals every value under the active
// key, ResealBatch rows per transaction, until a walk finds nothing; then it
// marks the old key retired, walks again with the retired key loaded, and
// deletes it. Rewrap moves every data key from one KEK to another, one row
// per transaction, touching no value. Each change to encryption_keys
// notifies Changed on infraweave_invalidate; Watch reloads the keyring on
// it, and Reload keeps the keys already loaded when the new ones do not
// unwrap.
//
// ref: decision 0013
//
// # Input and output
//
// Load reads and, on a first start, inserts encryption_keys, the platform
// stream's table, and calls the provider once per key. Rotate, Reseal and
// Rewrap write encryption_keys, and Reseal every column of Registry.
// Database errors that mean PostgreSQL is unreachable are marked with
// kernel.ErrUnavailable. Sealing and opening touch neither the database
// nor the provider. Watch logs a reload through the given slog.Logger;
// nothing else is logged.
//
// # Dependencies
//
// It imports platform/seal for the formats, crypt/kek for the KEK,
// platform/sql for errors, platform/sql/notify for the invalidations and
// pgx for the queries. internal/app loads the keyring before any listener
// binds, runs the rotation's jobs and infraweave secrets; internal/agent
// never imports it.
package crypt
