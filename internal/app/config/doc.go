// Package config is the configuration infraweave's process modes read.
//
// Config holds every setting, keyed as in the YAML file; Server holds the
// listener settings of httpkit/serve, squashed to server.listen,
// server.admin_listen and server.shutdown, beside decision 0003's
// server.rate_limit and server.idempotency; Database holds decision 0005's
// database.* settings of platform/sql. Defaults are what an empty
// configuration means, Validate rejects a setting the server could not start
// with, ServeFlags declares the serve process mode's flags, and Load decodes
// them.
//
// Settings come from flags, INFRAWEAVE_* environment variables and a YAML
// file named by --config, in that order of precedence. The file is strict:
// a key infraweave does not know fails the command rather than being
// ignored, so a misspelt setting never silently falls back to its default.
//
// # Input and output
//
// Load reads the parsed flag set, the INFRAWEAVE_* environment variables and,
// when --config names one, that YAML file. It writes nothing; every failure is
// returned as an error prefixed config:, bind flags: or read config:.
//
// # Dependencies
//
// It imports viper and pflag for the decoding, and the setting and default
// types of platform/httpkit/serve, middleware/ratelimit,
// middleware/idempotency and platform/sql. internal/app and cmd/ import it.
package config
