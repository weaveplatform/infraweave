// Package lifecycle follows native repository identities and GitHub App
// installation availability. It repoints matching stacks before delivery fan-out
// and retains removed membership independently of the current repository map.
//
// Its inputs are normalized VCS events and API-resolved identities; its outputs
// are account/integration-scoped repository, membership and installation records
// plus the Stacks-owned repoint operation. It depends on kernel, platform/tx,
// platform/queue, stacks/api and vcs/api; it makes no provider calls.
package lifecycle
