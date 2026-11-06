package crypt

import "context"

// ValueOf wraps b as an opened value.
func ValueOf(b []byte) Value { return Value{b} }

// SetResealBatch makes each reseal transaction rewrite n rows, until the
// test ends.
func SetResealBatch(t interface{ Cleanup(func()) }, n int) {
	resealBatch = n
	t.Cleanup(func() { resealBatch = ResealBatch })
}

// AfterResealBatch runs f after each committed reseal batch, until the
// test ends; an error f returns stops the reseal.
func AfterResealBatch(t interface{ Cleanup(func()) }, f func(context.Context) error) {
	afterBatch = f
	t.Cleanup(func() { afterBatch = func(context.Context) error { return nil } })
}
