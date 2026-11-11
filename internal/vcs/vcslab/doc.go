// Package vcslab describes the repositories the scheduled vcs-lab job
// provisions on each real provider: the history test/terraform/vcs/seed.sh
// pushes to every one, whose commit hashes are fixed because the script
// fixes every date and reads no configuration, and the environment
// variables through which the job names one provider's lab to the tests.
//
// ref: decision 0016
//
// # Input and output
//
// FromEnv reads the variables and reports ErrNotConfigured, naming each
// required one that is unset; nothing else reads or writes anything.
//
// # Dependencies
//
// It imports the standard library alone. Each provider's cloud test and
// the acceptance suite's @vcs-lab steps read it; no production code
// imports it.
package vcslab
