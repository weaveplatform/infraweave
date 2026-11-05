package seal

import "testing"

// SetMaxSegments lowers the segment limit for t, which no test could
// reach at 2^32.
func SetMaxSegments(t *testing.T, n uint64) {
	old := maxSegments
	maxSegments = n
	t.Cleanup(func() { maxSegments = old })
}
