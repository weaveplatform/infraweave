package rego

import (
	"crypto/fips140"
	"slices"

	"github.com/open-policy-agent/opa/v1/ast"
	oparego "github.com/open-policy-agent/opa/v1/rego"
	"github.com/open-policy-agent/opa/v1/types"

	"github.com/weaveplatform/infraweave/internal/iac/sanitize"
)

func capabilities() *ast.Capabilities { return capabilitiesFor(fips140.Enforced()) }

func capabilitiesFor(fips bool) *ast.Capabilities {
	caps := ast.CapabilitiesForThisVersion()
	// A library upgrade must not give an existing future.keywords wildcard
	// the negation semantics or logical operators of a later language version.
	caps.FutureKeywords = languageKeywords()
	caps.Features = slices.DeleteFunc(slices.Clone(caps.Features), func(feature string) bool {
		return feature == ast.FeatureRegoV2 || feature == ast.FeatureRegoV2Import
	})
	removed := []string{"http.send", "net.lookup_ip_addr", "opa.runtime", "rego.parse_module", "time.now_ns", "trace", "exec"}
	if fips {
		removed = append(removed, "crypto.md5", "crypto.sha1", "crypto.hmac.md5", "crypto.hmac.sha1")
	}
	caps.Builtins = slices.DeleteFunc(slices.Clone(caps.Builtins), func(b *ast.Builtin) bool { return slices.Contains(removed, b.Name) })
	caps.Builtins = append(caps.Builtins, &ast.Builtin{Name: "sanitized", Decl: sanitizedDecl()})
	// No capability may open a network request even through a future resolver.
	caps.AllowNet = []string{}
	return caps
}
func sanitizedDecl() *types.Function { return types.NewFunction(types.Args(types.A), types.A) }
func sanitized(_ oparego.BuiltinContext, term *ast.Term) (*ast.Term, error) {
	if value, ok := term.Value.(ast.String); ok {
		return ast.StringTerm(sanitize.Value(string(value)).(string)), nil
	}
	return term, nil
}

// languageKeywords are the opt-in keywords of the supported v0/v1 contract.
func languageKeywords() []string { return []string{"contains", "every", "if", "in"} }
