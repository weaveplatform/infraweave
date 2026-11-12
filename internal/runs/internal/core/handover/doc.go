// Package handover builds a claimed run's specification, the runs
// domain's side of the worker API's GET /runs/{run}/spec. Service's
// Handover finds the run by its ID alone, as a run token names it, and
// refuses one that is not on a private pool's worker. It reads the run's
// stack, its labels and its space's slug, and the stack's environment,
// opened, and renders each variable's ${{ … }} templates against the
// environment surface: env, the variables as saved, stack and run. A
// write-only value, or one whose template reads a write-only variable,
// goes into the secrets, with the in-run API token it mints and the run's
// artifact key; every other value, the TF_VAR_infraweave_ variables a
// variable of the same name overrides, the hooks, the runtime
// configuration and the runner image the run or its stack names go into
// the Spec in clear. The workers domain seals the secrets to the pool's
// key.
//
// ref: decision 0008 § "The run specification and its secrets"
// ref: decision 0013 § "Delivery to runs"
// ref: decision 0024 "Surfaces and their variables", "Determinism"
//
// # Input and output
//
// New takes the Store, the stacks domain's Stacks, the access domain's
// Spaces and in-run token minting, and the configuration domain's
// Environment; BindExpressions takes the expression registry. Handover
// answers a workersapi.Handover, or workersapi.ErrNotOnWorker, or a
// *kernel.Invalid for a template that fails to render.
//
// # Dependencies
//
// It imports the access, configuration, stacks and workers api packages,
// internal/kernel, platform/crypt, platform/expr, platform/tx and the
// domain's lifecycle and surface packages. The domain's assembly
// constructs it over the pg adapter.
package handover
