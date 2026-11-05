package acceptance

import (
	"regexp"
	"testing"
)

// pinned is an image reference deps-refresh.yml can move: a release tag
// and the digest of the image's multi-platform index.
var pinned = regexp.MustCompile(`^[a-z0-9./-]+:(\d+)[0-9.]*@sha256:[0-9a-f]{64}$`)

// TestUnitImagesPinned checks every image is pinned by digest, and that each
// PostgreSQL pin stays on the major its tests depend on: the floor, and the
// major below it that must be refused.
func TestUnitImagesPinned(t *testing.T) {
	for _, tc := range []struct {
		name, image, major string
	}{
		{"ImagePostgres", ImagePostgres, "18"},
		{"ImagePostgresBelowFloor", ImagePostgresBelowFloor, "17"},
		{"ImageObjectStore", ImageObjectStore, ""},
	} {
		m := pinned.FindStringSubmatch(tc.image)
		switch {
		case m == nil:
			t.Errorf("%s = %q is not pinned by a release tag and digest", tc.name, tc.image)
		case tc.major != "" && m[1] != tc.major:
			t.Errorf("%s = %q, want PostgreSQL %s", tc.name, tc.image, tc.major)
		}
	}
}
