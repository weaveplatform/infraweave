// Package api is what other domains read of the state domain: Versions,
// which reads the metadata of the current Version of a stack's workspace
// and the Outputs of its default workspace's, which DefaultWorkspace
// names.
//
// # Input and output
//
// None; it declares types and an interface.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The domains above
// state in the declared graph import it.
package api
