// Package session is decision 0006's sessions: the row behind every
// credential but the federation, worker and run tokens. A Session works in
// at most one account, names its actor, channel and expiry, and is Live
// until it is revoked or ends; Principal is who its credential acts as.
// A browser session is named by a cookie, CookieName, whose value
// NewSecret makes and whose SHA-256, HashSecret, is all the row stores.
// Admission is the port that decides which accounts admit a member, for
// the account switcher.
//
// Revocation reaches every replica through infraweave_invalidate: Key is
// the message session:<id>, which ParseKey reads back. Cache keeps a
// replica's sessions for CacheTTL, 60 seconds, so a lost message delays a
// revocation by at most that long; Drop forgets one session and Flush all.
//
// # Input and output
//
// None of its own: Cache reads through the loader and the clock it is
// given. Nothing is logged.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The access
// domain's authn service and pg adapter use it.
package session
