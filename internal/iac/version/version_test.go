package version_test

import (
	"cmp"
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

func TestUnitCompare(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.5", "1.5.7",
		"1.6.0-beta1", "1.6.0", "1.10.0", "2",
	}
	for i, a := range ordered {
		va, err := version.Parse(a)
		if err != nil {
			t.Fatal(err)
		}
		for j, b := range ordered {
			vb, _ := version.Parse(b)
			if got, want := va.Compare(vb), cmp.Compare(i, j); got != want {
				t.Errorf("%s vs %s = %d, want %d", a, b, got, want)
			}
		}
	}
	a, _ := version.Parse("1.5.0+one")
	b, _ := version.Parse("1.5+two")
	if a.Compare(b) != 0 || a.String() != "1.5.0" || b.String() != "1.5" {
		t.Errorf("metadata ordered, or String = %q, %q", a, b)
	}
	if v, _ := version.Parse("1.6.0-rc1+x"); v.String() != "1.6.0-rc1" {
		t.Errorf("String = %q", v)
	}
}

func TestUnitAllows(t *testing.T) {
	for _, c := range []struct {
		constraints string
		allowed     []string
		refused     []string
	}{
		{"1.5.7", []string{"1.5.7", "1.5.7+meta"}, []string{"1.5.6", "1.5.8"}},
		{"= 1.5", []string{"1.5.0"}, []string{"1.5.1"}},
		{"!= 1.5.7", []string{"1.5.6", "1.6.0"}, []string{"1.5.7"}},
		{"> 1.5.7", []string{"1.5.8"}, []string{"1.5.7", "1.6.0-beta1"}},
		{"<= 1.5.7", []string{"1.5.7", "1.0.0"}, []string{"1.5.8", "1.5.7-rc1"}},
		{">= 1.5.0, < 1.6.0", []string{"1.5.0", "1.5.7"}, []string{"1.6.0", "1.4.9"}},
		{"~> 1.5", []string{"1.5.0", "1.9.3"}, []string{"1.4.9", "2.0.0"}},
		{"~> 1.5.0", []string{"1.5.0", "1.5.9"}, []string{"1.6.0", "1.4.0"}},
		{"~> 1", []string{"1.0.0", "3.1.0"}, []string{"0.15.5"}},
		{">= 1.6.0-beta1", []string{"1.6.0-beta2", "1.6.0", "1.7.0"}, []string{"1.7.0-rc1", "1.6.0-alpha"}},
	} {
		cs, err := version.ParseConstraints(c.constraints)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []bool{true, false} {
			list := c.allowed
			if !want {
				list = c.refused
			}
			for _, s := range list {
				v, _ := version.Parse(s)
				if cs.Allows(v) != want {
					t.Errorf("%q allows %s = %v, want %v", c.constraints, s, !want, want)
				}
			}
		}
	}
}
