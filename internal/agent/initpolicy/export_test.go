package initpolicy

import "time"

// WithTimeout answers p with an evaluation budget of d. Load bounds the
// policy's compilation by its budget too, so a test that measures a
// short budget loads under a long one and evaluates under this.
func WithTimeout(p *Policy, d time.Duration) *Policy {
	q := *p
	q.timeout = d
	return &q
}
