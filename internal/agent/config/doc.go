// Package config is the configuration infraweave launcher reads.
//
// Config holds every launcher.* setting: the pool token and the pool's
// private key, the poll's hold, the default runner image, the cache
// directory, the worker's metadata, the admin listener's address, the
// launcher.log.* settings of platform/observe, and the run containers'
// launcher.docker.* network and runtime. Defaults are what an empty
// configuration means, Validate rejects a setting the launcher could not
// start with, Flags declares the launcher's flags, and Load decodes them.
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
