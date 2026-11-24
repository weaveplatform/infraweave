// Package ctl is infraweavectl's command tree: profile, whoami, version,
// api, operator with tier, organization, grant and billing, and preview
// with show and the account's and the member's own opt-in. Root builds
// it; cmd/infraweavectl runs it.
//
// A command whose annotation names a feature preview, or whose ancestor's
// does, is hidden from help and refused with ErrPreviewInactive unless
// featurePreview answers that preview active for the caller: the account
// has opted in and, for a member, the member has not opted out. Help and
// every other command call nothing for it while no command carries the
// annotation.
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
// more prints its problem and returns the SDK's typed error. A command a
// feature preview gates reads featurePreview before it runs, and help
// reads it to hide such a command.
//
// # Dependencies
//
// It imports pkg/sdk, internal/platform/buildinfo and
// internal/platform/private, cobra, viper and yaml.v3, and no pkg/client package or server code: depguard's ctl rule
// holds it to the API's public client.
package ctl
