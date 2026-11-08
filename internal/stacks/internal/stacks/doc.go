// Package stacks is the stacks domain's logic: Service, which reads a
// stack for other domains through stacksapi.Stacks, and is the domain's
// accessapi.SpaceGuard, refusing deleting a space a stack lives in. It
// refuses no move yet, since nothing a stack reaches through its space
// is attached to it.
//
// # Input and output
//
// None of its own: it reaches PostgreSQL through its Store.
//
// # Dependencies
//
// It imports accessapi, stacksapi and internal/kernel. The stacks domain's
// assembly constructs it over the pg package's Store.
package stacks
