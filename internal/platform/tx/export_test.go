package tx

import "time"

// SetBackoff replaces the wait before a retry until the test ends.
func SetBackoff(t interface{ Cleanup(func()) }, d time.Duration) {
	old := backoff
	backoff = func() time.Duration { return d }
	t.Cleanup(func() { backoff = old })
}
