// Package s3 is the objectstore driver for AWS S3 and S3-compatible
// stores, such as RustFS, Ceph RGW and Cloudflare R2.
//
// New loads the SDK's default credential chain, workload identity
// included, for objectstore.s3's region, endpoint and path style, and
// turns off the SDK's own request checksums, so no request carries an
// MD5 or a checksum the driver did not choose. Put sends an object up to
// the part size as one PutObject with x-amz-checksum-sha256, which the
// store verifies, and a larger one as a multipart upload whose parts each
// carry theirs; the upload completes only once the source has given
// exactly the declared size, and is aborted otherwise, even after ctx
// ends. Every write carries the configured server-side encryption, none,
// s3 or kms with its key. SignURL presigns GetObject or PutObject with
// SigV4; a PUT binds Content-Length, the encryption and, when given, the
// SHA-256, which the client sends among the returned headers.
//
// # Input and output
//
// It sends HTTPS requests to the S3 endpoint; a 404 is
// objectstore.ErrNotFound, and other errors name the operation and key.
// Nothing is logged.
//
// # Dependencies
//
// It imports aws-sdk-go-v2's s3 client and config, and objectstore.
// internal/app opens it for objectstore.driver s3.
package s3
