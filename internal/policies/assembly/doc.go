// Package assembly constructs the policies domain's contract publisher.
//
// # Input and output
//
// New returns a domain whose Routes publishes the typed policy JSON contract.
// internal/app mounts it on the guarded API listener.
//
// # Effects
//
// Construction opens no connections and starts no background work. Publication
// reuses immutable generated schema bytes and changes no stored state.
//
// # Dependencies
//
// chi and policies/internal/http.
package assembly
