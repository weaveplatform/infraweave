package kernel_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitCheckLabels(t *testing.T) {
	many := make([]string, kernel.MaxLabels+1)
	for i := range many {
		many[i] = "l" + strings.Repeat("x", i)
	}
	for _, tc := range []struct {
		name   string
		labels []string
		want   []kernel.FieldError
	}{
		{"none", nil, nil},
		{"distinct by case", []string{"Prod", "prod", "autoattach:x", "ünï"}, nil},
		{"longest", []string{strings.Repeat("é", kernel.MaxLabelLength)}, nil},
		{"empty", []string{""}, []kernel.FieldError{{Pointer: "/labels/0", Reason: kernel.ReasonMinLength}}},
		{
			"too long",
			[]string{"a", strings.Repeat("é", kernel.MaxLabelLength+1)},
			[]kernel.FieldError{{Pointer: "/labels/1", Reason: kernel.ReasonMaxLength}},
		},
		{"not UTF-8", []string{"\xff"}, []kernel.FieldError{{Pointer: "/labels/0", Reason: kernel.ReasonFormat}}},
		{"control", []string{"a\tb"}, []kernel.FieldError{{Pointer: "/labels/0", Reason: kernel.ReasonPattern}}},
		{"leading space", []string{" a"}, []kernel.FieldError{{Pointer: "/labels/0", Reason: kernel.ReasonPattern}}},
		{"trailing space", []string{"a "}, []kernel.FieldError{{Pointer: "/labels/0", Reason: kernel.ReasonPattern}}},
		{"duplicate", []string{"a", "b", "a"}, []kernel.FieldError{{Pointer: "/labels/2", Reason: kernel.ReasonDuplicate}}},
		{"too many", many, []kernel.FieldError{{Pointer: "/labels", Reason: kernel.ReasonMaxLength}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := kernel.CheckLabels("/labels", tc.labels)
			for i := range got {
				if got[i].Message == "" {
					t.Errorf("%s has no message", got[i].Pointer)
				}
				got[i].Message = ""
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CheckLabels = %+v, want %+v", got, tc.want)
			}
		})
	}
}
