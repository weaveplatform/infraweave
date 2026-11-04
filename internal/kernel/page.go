package kernel

const (
	// DefaultPageLimit is the page size when a request names none.
	DefaultPageLimit = 20
	// MaxPageLimit is the largest page.
	MaxPageLimit = 100
)

// PageLimit is the page size for a requested limit: DefaultPageLimit when
// absent, otherwise clamped to 1 to MaxPageLimit, never rejected.
func PageLimit(requested *int32) int32 {
	if requested == nil {
		return DefaultPageLimit
	}
	return min(max(*requested, 1), MaxPageLimit)
}

// Cursor is the position a list cursor holds, the last row of a page: its
// sort key and its ID, the tie-breaker that makes the order total. A list
// reads WHERE (sort_key, id) > (cursor) ORDER BY sort_key, id; on the wire a
// cursor is opaque.
type Cursor[K any] struct {
	Key K
	ID  string
}
