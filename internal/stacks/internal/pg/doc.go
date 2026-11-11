// Package pg is the stacks domain's PostgreSQL storage: Stream, its
// migration stream, whose stacks table keys each stack by its account and
// ULID, with a slug unique per account, its settings and its lock, the
// runs domain's projection of its run state, which SetRunState writes
// without counting a version, and the ID of the space it lives in, which
// carries no foreign key, since spaces belong to the access domain;
// stack_hooks, stack_labels and stack_stars hold its hooks, labels and
// each caller's star. Store reads a stack, by
// ID, by slug or by the repository's branch it tracks,
// stacksapi.ErrNotFound when the account lacks it, writes it whole but
// for its tracked commit and branch head, which SetTracked writes without
// its version, detaches the stacks bound to a VCS integration, which
// DetachVCS does without their versions,
// searches an account's stacks and suggests their values by
// stacks.Fields through platform/sql/search, answers whether a stack
// lives in a space, finds an account's stacks in some spaces by label
// through stack_labels_label, and holds a stack FOR KEY SHARE, the lock a
// key check takes, so a move or delete that locks it FOR UPDATE waits. A search joins the caller's stars, and reads the space
// tree from its scope, since the spaces tables belong to the access
// domain; a scope's Use keeps the stacks bound to its integration and,
// for a default, those on its provider bound to none. ByIDs reads the stacks of some IDs at once, InSpaces every
// stack in some spaces, and Labels the labels of some stacks. Purge deletes a
// batch of a deleted account's stacks, with their hooks, labels and
// stars, and reports whether none is left. Held names the columns that
// hold the access domain's accounts and spaces, and Keys declares the
// stacks' key, which other domains' held columns name. Every
// method joins the transaction its context carries, and a write of a taken
// slug is a *stacks.SlugTaken.
//
// # Input and output
//
// It reads and writes PostgreSQL through the DB it is given.
//
// # Dependencies
//
// It imports stacksapi, the domain's core package for its port,
// internal/kernel, kernel/search, platform/sql, platform/sql/integrity,
// platform/sql/migrate,
// platform/sql/search, platform/tx and pgx. The stacks domain's assembly
// constructs it.
package pg
