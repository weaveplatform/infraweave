// Package rawgit is the raw Git provider: Source implements port.Source
// and port.Brancher over Git's smart HTTP protocol for a public
// repository reached by its https URL with no credential. It lists
// branches and resolves a ref from the server's ref advertisement, fetches the commits it needs one commit deep into memory,
// and reads files and tree differences from them; the server never
// proxies an archive, and a run fetches its source from the repository's
// own URL.
//
// ref: decision 0012
//
// # Input and output
//
// It sends HTTP requests to the repository's host, each call bounded by
// Timeout and refusing a URL or redirect that is not https. A repository,
// ref or path the host lacks or denies is port.ErrNotFound, a host that
// does not answer is kernel.ErrUnavailable, and a file over
// port.MaxFileSize is port.ErrTooLarge. Git's SHA-1 object IDs come from
// go-git's sha1cd, an identifier with no security role, so no refused
// algorithm runs under fips140=only.
//
// # Dependencies
//
// It imports go-git's transport, protocol and object packages,
// internal/kernel, and the domain's api and port packages. The domain's
// assembly constructs it.
package rawgit
