package kernel_test

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitExpect(t *testing.T) {
	for _, tc := range []struct {
		name        string
		e           kernel.Expect
		version     int64
		stale, cond bool
	}{
		{"no If-Match", kernel.Expect{}, 7, false, false},
		{"any", kernel.ExpectAny(), 7, false, true},
		{"the version", kernel.ExpectVersions(6, 7), 7, false, true},
		{"another version", kernel.ExpectVersions(6), 7, true, true},
		{"no strong tag", kernel.ExpectVersions(), 7, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.e.Check(tc.version)
			if errors.Is(err, kernel.ErrStale) != tc.stale || (err != nil) != tc.stale ||
				tc.e.Conditional() != tc.cond {
				t.Errorf("Check = %v, Conditional = %v", err, tc.e.Conditional())
			}
		})
	}
}
