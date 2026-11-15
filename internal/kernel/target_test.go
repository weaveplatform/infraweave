package kernel_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// TestUnitCheckTargets accepts resource and module addresses with their
// keys, up to MaxTargets of up to MaxTarget characters, and refuses no
// address, too many, an overlong one and each that is not an address, at
// its pointer.
func TestUnitCheckTargets(t *testing.T) {
	long := "module.m[\"" + strings.Repeat("é", kernel.MaxTarget-12) + "\"]"
	ok := []string{
		"aws_instance.web",
		"terraform_data.this",
		"aws_instance.web[0]",
		`aws_instance.web["a b"]`,
		`aws_instance.web["$(touch x); 'q' \"esc\""]`,
		"data.aws_ami.base",
		"module.app",
		"module.app[2]",
		`module.app["eu"].module.db.aws_db_instance.main`,
		"my-type_2.name-1",
		long,
	}
	if errs := kernel.CheckTargets("/targets", ok); errs != nil {
		t.Errorf("refused: %v", errs)
	}
	bad := []string{
		"",
		"aws_instance",
		"aws_instance.",
		".web",
		"aws_instance.web.extra",
		"1aws.web",
		"aws_instance.web[a]",
		`aws_instance.web["a"`,
		`aws_instance.web["a"]x`,
		"aws_instance.web[0][1]",
		`aws_instance.web["a` + "\n" + `"]`,
		`aws_instance.web["a` + "\x00" + `"]`,
		"aws_instance.web; rm -rf /",
		"$(touch x).web",
		"aws_instance web",
		"module.",
		"module.app.",
		"-target=aws_instance.web",
	}
	errs := kernel.CheckTargets("/targets", bad)
	for i, b := range bad {
		want := kernel.FieldError{Pointer: fmt.Sprintf("/targets/%d", i), Reason: kernel.ReasonRule}
		if !slices.ContainsFunc(errs, func(e kernel.FieldError) bool {
			return e.Pointer == want.Pointer && e.Reason == want.Reason
		}) {
			t.Errorf("%q not refused as %s: %v", b, want.Reason, errs)
		}
	}
	if len(errs) != len(bad) {
		t.Errorf("%d refusals for %d addresses: %v", len(errs), len(bad), errs)
	}
	for name, c := range map[string]struct {
		targets []string
		want    kernel.FieldError
	}{
		"none": {nil, kernel.FieldError{Pointer: "/targets", Reason: kernel.ReasonMinLength}},
		"too many": {
			slices.Repeat([]string{"a.b"}, kernel.MaxTargets+1),
			kernel.FieldError{Pointer: "/targets", Reason: kernel.ReasonMaxLength},
		},
		"overlong": {
			[]string{"a.b", "a." + strings.Repeat("b", kernel.MaxTarget-1)},
			kernel.FieldError{Pointer: "/targets/1", Reason: kernel.ReasonMaxLength},
		},
	} {
		got := kernel.CheckTargets("/targets", c.targets)
		if len(got) != 1 || got[0].Pointer != c.want.Pointer || got[0].Reason != c.want.Reason {
			t.Errorf("%s: %v, want one %s at %s", name, got, c.want.Reason, c.want.Pointer)
		}
	}
	if errs := kernel.CheckTargets("/targets",
		slices.Repeat([]string{"a.b"}, kernel.MaxTargets)); errs != nil {
		t.Errorf("%d addresses refused: %v", kernel.MaxTargets, errs)
	}
}
