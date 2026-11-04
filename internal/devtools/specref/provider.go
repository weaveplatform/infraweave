package specref

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// providerPrefix is the Terraform type prefix of every Spacelift resource.
const providerPrefix = "spacelift_"

// TerraformOperations records which GraphQL root fields each Terraform
// resource and data source calls. The provider declares its operations as
// struct tags (`graphql:"stackCreate(input: $input)"`) in the file that
// implements the resource, so the file name gives the Terraform type.
type TerraformOperations struct {
	Resources   map[string][]string `json:"resources"`
	DataSources map[string][]string `json:"dataSources"`
}

// ReadProviderTarball scans the provider source for GraphQL operations.
// Every graphql tag is collected; the caller keeps those naming a root field,
// which drops nested selections such as `graphql:"localPreviewEnabled"`.
func ReadProviderTarball(tgz []byte) (*TerraformOperations, error) {
	ops := &TerraformOperations{
		Resources:   map[string][]string{},
		DataSources: map[string][]string{},
	}
	err := walkTarball(tgz, func(p string, content []byte) error {
		dir, file := path.Split(p)
		if dir != "spacelift/" || strings.HasSuffix(file, "_test.go") ||
			!strings.HasSuffix(file, ".go") {
			return nil
		}
		var target map[string][]string
		var name string
		switch {
		case strings.HasPrefix(file, "resource_"):
			target, name = ops.Resources, strings.TrimPrefix(file, "resource_")
		case strings.HasPrefix(file, "data_"):
			target, name = ops.DataSources, strings.TrimPrefix(file, "data_")
		default:
			return nil
		}
		fields, err := graphqlTagFields(p, content)
		if err != nil {
			return err
		}
		if len(fields) > 0 {
			target[providerPrefix+strings.TrimSuffix(name, ".go")] = fields
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ops, nil
}

// graphqlTagFields returns the sorted, de-duplicated field names named by
// graphql struct tags in a Go source file.
func graphqlTagFields(filename string, src []byte) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}
	seen := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		field, ok := n.(*ast.Field)
		if !ok || field.Tag == nil {
			return true
		}
		raw, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			return true
		}
		tag := reflect.StructTag(raw).Get("graphql")
		name, _, _ := strings.Cut(tag, "(")
		if name = strings.TrimSpace(name); name != "" && !strings.HasPrefix(name, "...") {
			seen[name] = true
		}
		return true
	})
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// RetainRootFields drops every recorded field that is not a root query or
// mutation of s, and resources left without operations.
func (t *TerraformOperations) RetainRootFields(s *Schema) {
	roots := map[string]bool{}
	for _, f := range s.Queries() {
		roots[f.Name] = true
	}
	for _, f := range s.Mutations() {
		roots[f.Name] = true
	}
	for _, m := range []map[string][]string{t.Resources, t.DataSources} {
		for k, fields := range m {
			kept := fields[:0]
			for _, f := range fields {
				if roots[f] {
					kept = append(kept, f)
				}
			}
			if len(kept) == 0 {
				delete(m, k)
			} else {
				m[k] = kept
			}
		}
	}
}
