// Package workspace saves a run's workspace for the run's next container
// and restores it there, end to end: only the pool's private key opens it.
// Write archives the workspace as tar, compresses it with zstd and seals
// it in platform/seal's object stream under a fresh content key, which
// travels in the pool envelope to the pool's public key, bound to the run
// and the state version it was saved at:
//
//	"IWWS" | 0x01 | state version (uint64) | envelope length (uint16) | envelope | object stream
//
// The providers of each .terraform directory are left out, since init
// installs them again from the lock file; so are a link that leaves the
// workspace, a device, a pipe and a socket, which Write answers by path.
// A file, link or name that holds one of the run's credentials the
// runner names, its run token and its source credential, fails the save.
// Read restores a saved workspace into a new directory, reading the
// stream to its end so any alteration fails it, and Extract, which Read
// and a local preview's archive share, writes a tar through an os.Root,
// refusing an entry or a link that leaves the directory and any device or
// pipe.
//
// ref: decision 0008 § "The run specification and its secrets"
// ref: decision 0013 § "Delivery to runs"
//
// # Input and output
//
// Write reads the workspace directory and writes to an io.Writer; Read
// reads an io.Reader and writes a new directory. Nothing is logged.
//
// # Dependencies
//
// It imports platform/seal and klauspost/compress's zstd. The runner
// saves a workspace, and internal/agent/source restores it and extracts
// a local preview's archive with it.
package workspace
