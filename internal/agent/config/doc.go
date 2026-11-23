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
// platform/observe, the launcher variables runs receive,
// launcher.whitelist_envs, whose ro_ and wo_ prefixes scope a variable to
// the read or the write phases under the name without it, and the run
// containers' launcher.docker.* network, runtime, runner binary and
// extra mounts: extra_mounts every phase sees read-only, and
// ro_extra_mounts and wo_extra_mounts, host files the read or the write
// phases alone see, each <host path>:<container path>, optionally with
// :ro, which ParseMount reads and Docker.Mounts answers. Validate refuses
// a whitelisted name that is not a variable's, that the runner sets
// itself, or that one phase would see twice, an extra mount that does not
// parse, and container paths internal/agent/phaseinputs refuses. Defaults are what
// an empty configuration means, Validate rejects a setting the launcher
// could not start with, Flags declares the launcher's flags, and Load
// decodes them. The default runner image is unset: when neither
// the specification nor the launcher names one, VendorRunnerImage selects
// the immutable image for OpenTofu, Pulumi or Ansible, otherwise Terraform's.
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
// It imports viper, pflag, platform/observe's log settings and
// internal/agent/phaseinputs' checks, and no
// server package (repo layout, agent-isolation). internal/agent/cli calls
// it.
package config
