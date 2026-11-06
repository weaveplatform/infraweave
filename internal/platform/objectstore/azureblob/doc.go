// Package azureblob is the objectstore driver for Azure Blob Storage.
//
// New authenticates through azidentity's default chain, Entra workload
// identity included, or, as Azurite's tests do, with an account key. Put
// sends an object up to the part size as one Put Blob and a larger one as
// staged blocks, each carrying the CRC64 the service checks, committed by
// a block list only once the source has given exactly the declared size;
// a failed write leaves only uncommitted blocks, which no reader sees and
// Azure discards. SignURL signs a blob SAS, read for a GET and create and
// write for a PUT, with a user delegation key, which it fetches for a day
// and reuses while it outlasts the URL, or with the account key. A SAS
// binds neither the size nor a checksum, so blobs checks both when it
// commits a signed upload. A PUT sends x-ms-blob-type, which the returned
// headers carry.
//
// # Input and output
//
// It sends HTTPS requests to the account URL, or HTTP to an emulator's;
// a 404 is objectstore.ErrNotFound, and other errors name the operation
// and key. Nothing is logged.
//
// # Dependencies
//
// It imports the azblob SDK, azidentity and objectstore. internal/app
// opens it for objectstore.driver azureblob.
package azureblob
