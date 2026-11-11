// Package gitmem is a Git repository held in memory by go-git for the
// providers' fakes: Repo commits files on branches, points branches,
// makes annotated tags, and answers what a provider's API reads of a
// repository, so refs, tags, file contents and the files two commits
// differ in behave as Git's do. Each commit is a minute after the one
// before it, so commits order the same on every run.
//
// # Input and output
//
// It writes and reads nothing outside memory. A ref, SHA or path the
// repository lacks is ErrNotFound; seeding panics on a go-git failure,
// which no test recovers from.
//
// # Dependencies
//
// It imports go-git with its in-memory storage and filesystem. The
// providers' fakes construct it; no production code imports it.
package gitmem
