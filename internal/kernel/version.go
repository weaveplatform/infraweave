package kernel

import (
	"errors"
	"slices"
)

// ErrStale is a conditional write's refusal: the If-Match the request
// carries names no version the entity has now, so the client read it
// before another write changed it. The HTTP layer answers it with 412
// precondition-failed.
//
// ref: decision 0022
var ErrStale = errors.New("stale version")

// Expect is the versions of an entity a conditional write accepts, read
// from an If-Match header. The zero Expect accepts any version, as a write
// without If-Match does: the last write wins.
type Expect struct {
	conditional bool
	any         bool
	versions    []int64
}

// ExpectAny accepts any version of an entity that exists, as If-Match: *
// does.
func ExpectAny() Expect { return Expect{conditional: true, any: true} }

// ExpectVersions accepts only the versions it lists; with none, it accepts
// nothing, as an If-Match naming only weak or unknown tags does.
func ExpectVersions(versions ...int64) Expect {
	return Expect{conditional: true, versions: slices.Clone(versions)}
}

// Conditional reports whether e came from an If-Match, so a write that
// loses a race to another is refused rather than retried.
func (e Expect) Conditional() bool { return e.conditional }

// Check is nil when e accepts version, and ErrStale otherwise.
func (e Expect) Check(version int64) error {
	if !e.conditional || e.any || slices.Contains(e.versions, version) {
		return nil
	}
	return ErrStale
}
