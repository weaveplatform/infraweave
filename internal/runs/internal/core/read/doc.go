// Package read reads runs as the API answers them: Service reads a run by
// its ID, alone or on its stack, a page of a stack's runs, proposed runs,
// tasks or runs created in a range, newest first, and a search of the runs
// of the stacks in the spaces the caller may read, with its suggestions
// and its total. Each run is a View with its stack, the stack's branch,
// its transitions, its comments, the Guards that say why it cannot be
// retried or promoted, and the actions the caller may take on it, which
// Allowed derives from the edges its state allows next, the stop and kill
// asked of it, its guards, and the actions the caller holds on the
// stack's space. RetryBlocker and PromoteBlocker answer the guards, which
// the control package checks again before it acts. SpaceOf finds a run's
// space for the route's decision, StackOf a run's stack, and RunAccess a
// run's stack, space and whether it plans, applies or performs, for its
// in-run API token's authentication. Store is its storage port.
// Standalone run projections read their base rows, related evidence and
// guards in one serializable transaction; transactional callers retain
// their existing transaction.
//
// # Input and output
//
// It starts transactions through tx.Beginner, reaches storage through
// Store, the stacks domain through stacksapi.Stacks and the access domain
// through accessapi.Spaces and accessapi.Authorizer.
//
// # Dependencies
//
// It imports accessapi, stacksapi, the domain's lifecycle and machine,
// internal/kernel, kernel/search and internal/platform/tx. The runs domain's assembly
// constructs it.
package read
