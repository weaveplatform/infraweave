// Package source writes a run's workspace on the launcher's host, before
// the run's container starts: Fetch its source, and Restore the workspace
// its planning saved. A git source's
// commit is fetched from the provider's HTTPS URL with go-git, alone and
// with no history, presenting the run's source credential as HTTP Basic
// auth, and checked out; the credential stays in memory, and an error
// that would echo it has it replaced. An archive source, a local
// preview's tar.gz, is downloaded from its pre-signed URL, at most 2 GiB,
// and extracted through an os.Root of the workspace, at most 8 GiB,
// refusing an entry or a link that leaves the workspace and any device
// or pipe. A saved workspace is downloaded from the pre-signed URL the
// worker API answered, at most 2 GiB, opened with the pool's private key
// and extracted the same way by internal/agent/workspace. The
// specification's project root must then be a directory of the
// workspace, which is handed to the run's user: owned by UID and GID 1983
// where the launcher may change owners, else open to every user.
//
// ref: decision 0008 § "The run specification and its secrets"
// ref: decision 0012 § "Source access for runs"
//
// # Input and output
//
// It reads the provider's Git server over HTTP(S), trusting the system's
// roots and the caller's PEM certificates, and an archive or a saved
// workspace through the caller's HTTP client, and writes the workspace
// directory it is given. Each error names the step and the source's host,
// never the credential or a signed URL.
//
// # Dependencies
//
// It imports go-git and internal/agent/wire and workspace.
// internal/agent/launcher calls it while it prepares a run.
package source
