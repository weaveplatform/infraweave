// Package kernel holds the value types every domain API shares, so that two
// domains exchange a cursor, an ID or a run state without importing each
// other.
//
// Today it holds decision 0003's pagination values: Cursor, the keyset
// position a list cursor carries, and PageLimit, which clamps a requested
// page size to DefaultPageLimit and MaxPageLimit. IDs, principals, run states
// and the other shared types join it with the phases that need them.
//
// # Input and output
//
// None. It parses and validates values and does nothing else: no network,
// file, clock, environment or logging.
//
// # Dependencies
//
// It imports only the standard library, so any package may depend on it,
// domain api/ packages included. Its wire encodings live elsewhere: the
// cursor's in platform/httpkit/page.
package kernel
