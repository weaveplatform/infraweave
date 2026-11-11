// Package surface is what the runs domain adds to the environment
// surface: run, a run's ID, type, commit, who triggered it and its
// priority, typed so a variable's template that misspells a field is
// refused when it is saved. The configuration domain owns the surface and
// describes its contexts, stacks, which have no run: an editor tries a
// template against a run it names in its sample.
//
// ref: decision 0024 "Who declares a surface"
//
// # Input and output
//
// Variable takes nothing and returns the run variable; Value takes a run
// and returns the value a template reads as run, as a run's handover
// renders it. Nothing is stored or logged.
//
// # Dependencies
//
// internal/platform/expr, internal/kernel and the runs core's lifecycle.
package surface
