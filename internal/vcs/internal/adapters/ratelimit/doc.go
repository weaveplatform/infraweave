// Package ratelimit decodes native provider retry headers without performing
// requests or choosing queue policy. Each adapter supplies its header precedence.
//
// # Input and output
//
// Reset reads response headers, the current time and the adapter's ordered
// header names. It returns the first valid reset time and whether one was found;
// malformed or overflowing hints are ignored.
//
// # Dependencies
//
// It uses only the standard library. Native VCS adapters call it when classifying
// refused requests; core feedback code retains ownership of queue scheduling.
package ratelimit
