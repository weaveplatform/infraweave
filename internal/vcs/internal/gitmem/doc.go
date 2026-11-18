// Package gitmem is a Git repository held in memory by go-git for the
// providers' fakes: Repo commits files on branches, points branches,
// makes annotated tags, and answers what a provider's API reads of a
// repository, so refs, tags, file contents and the files two commits
// differ in behave as Git's do. Each commit is a minute after the one
// before it, so commits order the same on every run. ServeUploadPack exposes
// these objects over read-only Git smart HTTP; each provider fake owns its
// authentication and repository authorization before calling it.
//
// # Input and output
//
// It keeps repository objects in memory and can serve them to an HTTP response.
// A ref, SHA or path the
// repository lacks is ErrNotFound; seeding panics on a go-git failure,
// which no test recovers from.
//
// # Dependencies
//
// It imports go-git with its in-memory storage and filesystem. The
// providers' fakes construct it; no production code imports it.
package gitmem
