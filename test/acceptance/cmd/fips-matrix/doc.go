// Package main emits CI jobs from the acceptance partition catalogue.
// Native/component acceptance and each regular scenario partition receive an
// independent bounded job; scheduled lab partitions retain their own jobs.
//
// # Dependencies
//
// The existing harness partition catalogue supplies every scenario selector.
// The command uses no services, credentials or additional dependencies.
//
// # Input and output
//
// No arguments or configuration are read. Standard output is one compact JSON
// GitHub Actions matrix containing a native job and every regular partition.
package main
