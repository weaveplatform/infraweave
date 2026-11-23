package features

import "testing"

// WithCatalogue compiles c in for the rest of t in place of the generated
// catalogue, so a test covers features the register does not hold, such as
// a preview. c must be in name order, as the generated catalogue is.
func WithCatalogue(t testing.TB, c []Feature) {
	t.Helper()
	saved := catalogue
	catalogue = c
	t.Cleanup(func() { catalogue = saved })
}
