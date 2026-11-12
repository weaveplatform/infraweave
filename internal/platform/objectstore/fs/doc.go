// Package fs is the objectstore driver for a local directory, or a POSIX
// volume every replica shares.
//
// New opens objectstore.fs.root through os.Root, so no key reaches
// outside it. Put writes to a temporary file under .tmp, syncs it, renames
// it into place and syncs the directory, so a reader sees the whole
// object or none and the rename survives a crash. SignURL returns
// <objectstore.fs.url>/blobs/<token>. The token holds the method, the key,
// the expiry and a PUT's size and SHA-256, under HMAC-SHA256 with the
// objectstore-url key derived from the keyring's active data key, whose
// ID it carries so a URL signed before a rotation still verifies. Routes
// serves GET and PUT Pattern, /blobs/{token}, on the API listener, where
// the token is the only credential: 403 for a token that is malformed,
// altered, expired or for the other method, 413 for a PUT above its size
// and 400 below it or without a length, and 422 when the bytes do not
// match the bound SHA-256, which the handler checks before the object
// becomes readable. A verified transfer's deadlines move to Stall past
// each read of an upload or write of a download, so an object of any size
// moves for as long as it keeps moving, and a transfer that stalls loses
// its connection.
//
// # Input and output
//
// It reads and writes files under the root, and answers HTTP on the
// routes it adds. A missing file is objectstore.ErrNotFound. Nothing is
// logged.
//
// # Dependencies
//
// It imports chi for the routes, httpkit/problem for the answers and
// objectstore; the keyring arrives as Keys. internal/app opens it for
// objectstore.driver fs and mounts its routes.
package fs
