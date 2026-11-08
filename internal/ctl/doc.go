// Package ctl is infraweavectl's command tree: profile, whoami, version,
// api, and operator with tier, organization, grant and billing. Root builds
// it; cmd/infraweavectl runs it.
//
// Settings are strict YAML profiles, each an endpoint and a credential (an
// API key's ID and secret, or a token), with flag > environment > file
// precedence: --profile, --endpoint and --api-key over INFRAWEAVECTL_PROFILE,
// INFRAWEAVECTL_ENDPOINT and INFRAWEAVECTL_API_KEY over the profile. The
// key's secret and a token have variables, INFRAWEAVECTL_API_KEY_SECRET and
// INFRAWEAVECTL_TOKEN, and standard input for profile set, but no flag, so
// neither appears in a process listing.
//
// ref: decisions 0001 and 0021
//
// # Input and output
//
// It reads the arguments, the environment, standard input and the profiles
// file, by default config.yaml under the user configuration directory's
// infraweavectl, which profile set writes readable by its owner only
// (platform/private). Every
// call goes to the server through pkg/sdk's Client.Do, and each command
// prints the answer's body, indented JSON, to standard output; a list
// command follows every cursor and prints one array. An answer of 400 or
// more prints its problem and returns the SDK's typed error.
//
// # Dependencies
//
// It imports pkg/sdk, internal/platform/buildinfo and
// internal/platform/private, cobra, viper and yaml.v3, and no pkg/client package or server code: depguard's ctl rule
// holds it to the API's public client.
package ctl
