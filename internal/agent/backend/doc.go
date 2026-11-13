// Package backend points a run at its stack's managed state backend.
// Configure, for a handoff whose specification names stateBackend, writes
// File at the run's project root with Terraform's and OpenTofu's backend
// "http" block, its state, lock and unlock addresses all the stack's
// backend URL, and answers the phase environment's credentials: Username
// and the run token, which no file in the workspace holds. Address joins
// the backend's path to the server URL the launcher reaches, keeping a
// relay's or a private address's host and port. Code that declares its
// own backend fails initialization, since the tool refuses two.
//
// ref: decision 0010 § "How a run reaches it"
// ref: decision 0008 § "The run specification and its secrets"
//
// # Input and output
//
// Configure takes the project root's directory and a handoff, writes one
// file there and answers environment entries as NAME=value strings.
// Nothing is logged.
//
// # Dependencies
//
// It imports internal/agent/client's BasePath, internal/agent/handoff and
// the standard library. The runner calls it before a run's first phase.
package backend
