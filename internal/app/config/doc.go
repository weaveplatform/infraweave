// Package config is the configuration infraweave's process modes read.
//
// Config holds every setting, keyed as in the YAML file; Server holds the
// listener settings of httpkit/serve, squashed to server.listen,
// server.admin_listen and server.shutdown, beside decision 0003's
// server.rate_limit and server.idempotency; Database holds decision 0005's
// database.* settings of platform/sql; Log holds decision 0017's log.*
// settings of platform/observe; Queue holds decision 0011's queue.*
// settings of platform/queue; Events holds decision 0015's events.*
// settings of platform/outbox; Secrets holds decision 0013's secrets.*
// settings of platform/crypt: secrets.rotation_interval, and the KEK
// provider and each provider's settings under secrets.kek, as in
// secrets.kek.vault.auth.method;
// ObjectStore holds
// decision 0005's objectstore.* settings of platform/objectstore, whose
// per-store keys are the stores' hyphenated names, as in
// objectstore.retention.run-logs and INFRAWEAVE_OBJECTSTORE_RETENTION_RUN_LOGS.
// Defaults are what an empty configuration
// means, Validate rejects a setting the server could not start with,
// ServeFlags declares the flags of the modes that serve the API, AdminFlags
// those of a mode that serves only the admin listener, FileFlag only
// --config for commands such as migrate, and Load decodes them. LoadKEK
// decodes a file of the keys under secrets.kek alone, the KEK infraweave
// secrets rewrap --to moves the data keys to, as strictly.
//
// Settings come from flags, INFRAWEAVE_* environment variables and a YAML
// file named by --config, in that order of precedence. The file is strict:
// a key infraweave does not know fails the command rather than being
// ignored, so a misspelt setting never silently falls back to its default.
// The one setting no file holds is the local KEK, which Load reads from
// INFRAWEAVE_SECRETS_KEK itself: bound as the key secrets.kek, the
// variable would hide every other key below it.
//
// # Input and output
//
// Load reads the parsed flag set, the INFRAWEAVE_* environment variables and,
// when --config names one, that YAML file; LoadKEK reads only its file. It writes nothing; every failure is
// returned as an error prefixed config: or read config:, or, from LoadKEK,
// KEK settings: or read KEK settings:.
//
// # Dependencies
//
// It imports viper and pflag for the decoding, and the setting and default
// types of platform/httpkit/serve, middleware/ratelimit,
// middleware/idempotency, platform/sql, platform/observe, platform/queue,
// platform/outbox, platform/crypt, crypt/kek and platform/objectstore.
// internal/app and cmd/ import it.
package config
