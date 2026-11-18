// Package lifecycle follows native repository identities and GitHub App
// installation availability. It repoints matching stacks before delivery fan-out
// and retains removed membership independently of the current repository map.
//
// # Input and output
//
// Inputs are normalized VCS events and API-resolved native identities, scoped to
// an account and integration. Outputs are repository, membership and installation
// records and the Stacks-owned repoint operation. It receives no credentials and
// makes no provider call. Source checks refuse only known unavailable access.
//
// # Dependencies
//
// It uses kernel, platform/tx and platform/queue, stacks/api, vcs/api and its own
// domain's port DTOs. The already pinned UUID parser equates native UUID spellings.
// The completion transaction serializes identity changes and fan-out together.
//
// ref: decision 0012 D15 and D22
package lifecycle
