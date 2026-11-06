// Package gcs is the objectstore driver for Google Cloud Storage, over
// its own JSON API rather than the S3 interoperability API, whose HMAC
// keys workload identity cannot replace.
//
// New uses Application Default Credentials. Put streams to a resumable
// upload in chunks of the part size, each carrying the CRC32C the service
// checks, and hashes the stream with SHA-256 as it goes; Cloud Storage
// verifies no SHA-256, so blobs reads a signed upload back to compare.
// Only closing the upload makes the object, so a source that gives other
// than the declared size cancels it and leaves nothing. SignURL signs a
// V4 URL, through IAM signBlob when the identity holds no private key; a
// PUT binds Content-Length as a signed header, and Cloud Storage can bind
// no SHA-256.
//
// # Input and output
//
// It sends HTTPS requests to Cloud Storage; a missing object is
// objectstore.ErrNotFound, and other errors name the operation and key.
// Nothing is logged.
//
// # Dependencies
//
// It imports cloud.google.com/go/storage and objectstore. internal/app
// opens it for objectstore.driver gcs.
package gcs
