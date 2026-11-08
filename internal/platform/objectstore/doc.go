// Package objectstore is infraweave's object storage: the eleven logical
// stores, the interfaces their drivers implement, and the router that puts
// each store under its prefix in its bucket.
//
// A Bucket is one driver's bucket, over object keys. New opens each
// distinct bucket the configuration names once, through the caller's
// open function, and returns a Store that writes store n's key k as
// <n>/<k> in n's bucket: objectstore.buckets.<store> when set, else the
// driver's own. Every call checks the store's name and the key
// (CheckKey), so no key escapes its prefix. Put streams exactly the
// declared size and returns the size and SHA-256 it stored; a source that
// gives more or less fails with ErrSize and leaves nothing readable. Get
// and Head of a missing key return ErrNotFound, and Delete of one
// succeeds. SignURL signs a GET or PUT for the configured TTL when given
// none, and returns the URL with the headers the client must send, such
// as S3's signed x-amz-checksum-sha256 or Blob Storage's x-ms-blob-type.
// Writer is the Store without Delete, as serve holds it; WriterOnly wraps
// a Store so it cannot be asserted back. ReadPart and AtEnd are the
// drivers' size checks for writes sent in parts. Config is the objectstore
// settings: Validate names a bad key, and Require refuses a missing
// driver, which every process mode needs.
//
// ref: decision 0005
//
// # Input and output
//
// The router calls its buckets and returns their errors, which name the
// driver and the key. Nothing here touches the database or logs.
//
// # Dependencies
//
// It imports only the standard library. The drivers live in s3, gcs,
// azureblob and fs; blobs records objects in PostgreSQL; internal/app
// opens the configured driver and gives serve WriterOnly of the Store.
package objectstore
