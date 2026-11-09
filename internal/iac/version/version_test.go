package version_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/version"
)

func TestUnitParseConstraints(t *testing.T) {
	v := func(prerelease, metadata string, segs ...int64) version.Version {
		return version.Version{Segments: segs, Prerelease: prerelease, Metadata: metadata}
	}
	for in, want := range map[string]version.Constraints{
		"1.6.0": {{Op: version.Exact, Version: v("", "", 1, 6, 0)}},
		"= 1.6": {{Op: version.Equal, Version: v("", "", 1, 6)}},
		"~>1":   {{Op: version.Pessimistic, Version: v("", "", 1)}},
		"!=1.5.2": {
			{Op: version.NotEqual, Version: v("", "", 1, 5, 2)},
		},
		" >= 1.5.0 , < 2.0.0-rc1 ": {
			{Op: version.GreaterEqual, Version: v("", "", 1, 5, 0)},
			{Op: version.Less, Version: v("rc1", "", 2, 0, 0)},
		},
		"> 1.0, <= 1.9.9-beta.2+build.7": {
			{Op: version.Greater, Version: v("", "", 1, 0)},
			{Op: version.LessEqual, Version: v("beta.2", "build.7", 1, 9, 9)},
		},
	} {
		got, err := version.ParseConstraints(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("ParseConstraints(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
}

func TestUnitParseConstraintsRefuses(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"latest",
		"1.x",
		"v1.6.0",
		"1.6.0.1",
		"=> 1.0",
		"~ 1.0",
		">= 1.0,",
		">= 1.0,, < 2.0",
		"1.0 2.0",
		"1.0-",
		"1.0+",
		"1.0-beta..1",
		"99999999999999999999.0",
	} {
		if got, err := version.ParseConstraints(in); !errors.Is(err, version.ErrSyntax) {
			t.Errorf("ParseConstraints(%q) = %+v, %v; want ErrSyntax", in, got, err)
		}
	}
}
