// Package runnerimages names the immutable runner environments shipped with
// this project revision. The agent, account-default reads and acceptance
// harness share these references so a warm Docker cache cannot retain an
// older mutable default.
//
// # Input and output
//
// deploy/images/images.lock.json supplies the verified multi-platform OCI
// index digests that imagectl.py generates into images.gen.go. The constants
// perform no runtime I/O. Runner environments carry the vendor tools or
// support filesystem; launchers mount the current application executable
// and each requested Terraform/OpenTofu executable independently.
//
// # Dependencies
//
// The generated constants import no packages. Agent configuration, worker
// account defaults and the acceptance harness consume this shared package.
//
// ref: decision 0008
// ref: decision 0020
package runnerimages
