// Package config is the configuration infraweave launcher reads.
//
// Config holds every launcher.* setting: the pool token and the pool's
// private key, the poll's hold, the default runner image, the cache
// directory, the state lock's wait, the run timeout and the no-log
// timeout, the variables whose values runs mask, whether runs may keep
// their sensitive outputs' values, the max_requests_per_second and
// max_requests_burst API rate settings exposed to run clients (10 and
// 100 by default, both positive), the worker's metadata, the admin
// listener's address, the launcher.log.* settings of
// platform/observe, and the run containers' launcher.docker.* network,
// runtime and runner binary. Defaults are what
// an empty configuration means, Validate rejects a setting the launcher
// could not start with, Flags declares the launcher's flags, and Load
// decodes them. The default runner image is unset: when neither
// the specification nor the launcher names one, VendorRunnerImage selects
// OpenTofu's image for OpenTofu and Terraform's for every other vendor.
// Key reads the private key, given as a path, as PEM or as base64 of PEM.
//
// Settings come from flags, INFRAWEAVE_LAUNCHER_* environment variables
// and a YAML file named by --config, in that order of precedence. The file
// is strict: a key the launcher does not know fails the start. Each
// INFRAWEAVE_LAUNCHER_METADATA_<key> variable adds a metadata entry.
//
// ref: decision 0008 § "Configuration"
//
// # Input and output
//
// Load reads the flags, the process environment and the file. Key reads
// the key file when the setting names one. Nothing is logged.
//
// # Dependencies
//
// It imports viper, pflag and platform/observe's log settings, and no
// server package (repo layout, agent-isolation). internal/agent/cli calls
// it.
package config
