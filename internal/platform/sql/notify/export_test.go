package notify

import "time"

// SetBackoff replaces the reconnect bounds until the test ends.
func SetBackoff(t interface{ Cleanup(func()) }, first, most time.Duration) {
	old := backoff
	backoff = [2]time.Duration{first, most}
	t.Cleanup(func() { backoff = old })
}
