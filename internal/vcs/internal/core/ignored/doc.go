// Package ignored owns the retained reasons a repository event created no run
// on a stack. It records tracked-branch path/cap ignores and independent tag
// ignores for every account, without evaluating policies or entitlements.
//
// # Input and output
//
// The fenced stack evaluator supplies immutable prepared commit metadata and
// its decision under the account/stack holds. Records are unique per account,
// stack, commit and reason. Admin searches resolve the stack's current space,
// then page its last seven days through the shared search contract. Stack and
// account deletion remove owned rows; hourly retention prunes older records.
// Errors preserve dependency and transaction identity; no provider write or
// secret is involved. The shared API gate controls only reading, never recording.
//
// # Dependencies
//
// This private VCS package imports kernel/search, platform transactions and the
// access and stacks APIs. VCS assembly supplies its store and forward ports;
// adapters own SQL and served views, and app registers the stack deletion guard.
package ignored
