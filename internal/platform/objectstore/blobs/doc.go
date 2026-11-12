// Package blobs keeps objects consistent with PostgreSQL through
// blob_objects: every object has a row, a row is live exactly when the
// business change that records it commits, and objects go only after the
// change that drops them commits.
//
// Objects is serve's side, over an objectstore.Writer. Begin records a
// pending row for a signed upload's declared size and SHA-256 before the
// URL is issued, and finds it again when the same declaration repeats;
// BeginSealed does so for an upload to a sealed store that its uploader
// sealed under a content key the server derives too. SignPut and SignGet
// sign the URLs, a GET only for a live object. Put records a row and
// writes the object itself, sealing a sealed store's object under a
// content key of its own. Each sealed content key goes into
// content_key_sealed with associated data
// blob_objects.content_key_sealed:<store>/<key>. Begin, BeginSealed and
// Put insert on the pool outside the caller's transaction, so a rollback
// leaves the row for blob.expire. Commit and CommitUpload run in the
// caller's transaction: CommitUpload checks the size with Head and, on gcs
// and azureblob, whose URLs bind no SHA-256, reads the object back to
// compare, and answers the size and SHA-256; a mismatch leaves the row
// pending. Open reads a live object, opening a sealed one.
// Delete marks the row deleting and enqueues blob.delete in the caller's
// transaction. Reaper is the scheduler's side, over the full Store:
// blob.delete removes the object and then the row, and blob.expire marks
// live rows past expires_at and pending rows older than a day deleting,
// a batch at a time, each with its blob.delete.
//
// ref: decision 0005
//
// # Input and output
//
// It reads and writes blob_objects, a platform-stream table, enqueues on
// the maintenance queue, and calls the store. Database errors that mean
// PostgreSQL is unreachable are marked with kernel.ErrUnavailable.
// Nothing is logged.
//
// # Dependencies
//
// It imports objectstore, crypt and seal for sealed stores, queue and tx
// for jobs and transactions, and pgx. internal/app builds Objects for
// serve and Reaper for the scheduler.
package blobs
