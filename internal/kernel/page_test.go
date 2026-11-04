package kernel

import (
	"math"
	"testing"
)

func TestUnitPageLimit(t *testing.T) {
	n := func(v int32) *int32 { return &v }
	for _, tc := range []struct {
		in   *int32
		want int32
	}{
		{nil, 20},
		{n(1), 1},
		{n(50), 50},
		{n(100), 100},
		{n(0), 1},
		{n(-5), 1},
		{n(101), 100},
		{n(math.MaxInt32), 100},
	} {
		if got := PageLimit(tc.in); got != tc.want {
			t.Errorf("PageLimit(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
