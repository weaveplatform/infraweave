package plan_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/iactest"
	"github.com/weaveplatform/infraweave/internal/iac/plan"
)

// TestUnitEntityOf splits root, module, data source and keyed addresses,
// a dot or bracket inside a quoted key included, and refuses what is not
// a resource address, such as one whose type would be module or data.
func TestUnitEntityOf(t *testing.T) {
	for address, want := range map[string]plan.Entity{
		"aws_s3_bucket.logs":       {Type: "aws_s3_bucket", Name: "logs"},
		"data.aws_ami.base":        {Type: "data.aws_ami", Name: "base"},
		"null_resource.each[0]":    {Type: "null_resource", Name: "each[0]"},
		`null_resource.key["a.b"]`: {Type: "null_resource", Name: `key["a.b"]`},
		`null_resource.key["]\"."]`: {
			Type: "null_resource", Name: `key["]\"."]`,
		},
		"module.net.aws_subnet.private": {
			Module: "module.net", Type: "aws_subnet", Name: "private",
		},
		`module.net["eu.west"].module.az[1].data.aws_zone.this`: {
			Module: `module.net["eu.west"].module.az[1]`, Type: "data.aws_zone", Name: "this",
		},
	} {
		got, ok := plan.EntityOf(address)
		if !ok || got != want {
			t.Errorf("EntityOf(%s) = %+v, %t, want %+v", address, got, ok, want)
		}
		if ok && got.Local() != want.Type+"."+want.Name {
			t.Errorf("%s: Local = %s", address, got.Local())
		}
	}
	for _, address := range []string{
		"", "aws_s3_bucket", "a.b.c", ".a.b", "a..b", "a.b.", "a.b[0", "a.b]", `a.b["x]`,
		"a[0].b", "a.[0]", "module.x", "data.a.b.c", "module.x.data.a",
		"module.module", "data.a",
	} {
		if got, ok := plan.EntityOf(address); ok {
			t.Errorf("EntityOf(%q) = %+v", address, got)
		}
	}
}

// TestUnitEntityOfRecorded splits every address of the tools' recorded
// plans.
func TestUnitEntityOfRecorded(t *testing.T) {
	for _, tool := range []string{"terraform", "tofu"} {
		cases, err := iactest.Recorded(filepath.Join("testdata", tool))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cases {
			p, err := plan.Parse(bytes.NewReader(c.Document))
			if err != nil {
				t.Fatal(err)
			}
			for _, ch := range p.Changes {
				if _, ok := plan.EntityOf(ch.Address); !ok {
					t.Errorf("%s %s: EntityOf(%s) refused", tool, c.Name, ch.Address)
				}
			}
		}
	}
}
