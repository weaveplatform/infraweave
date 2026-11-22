package catalogue

import (
	"fmt"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/format"
)

// Rewrite adapts only parsed namespace references. Imported data keeps its
// original local alias, so rule variables and arbitrary string literals retain
// their semantics. Sources and source digests remain unchanged in upstream.
func Rewrite(name, source string) (string, int, error) {
	module, err := ast.ParseModuleWithOpts(name, source, ast.ParserOptions{RegoVersion: ast.RegoV0})
	if err != nil {
		return "", 0, fmt.Errorf("catalogue: parse %s: %w", name, err)
	}
	changes := 0
	if len(module.Package.Path) == 2 {
		switch module.Package.Path[1].Value {
		case ast.String("spacelift"):
			module.Package.Path[1].Value = ast.String("infraweave")
			changes++
		case ast.String("spacelift_test"):
			module.Package.Path[1].Value = ast.String("infraweave_test")
			changes++
		}
	}
	for _, imported := range module.Imports {
		ref, ok := imported.Path.Value.(ast.Ref)
		if ok && len(ref) >= 2 && ref[0].Value == ast.Var("data") && ref[1].Value == ast.String("spacelift") && imported.Alias == "" {
			imported.Alias = ast.Var("spacelift")
		}
	}
	changes += rewriteInputDocuments(module)
	ast.WalkRefs(module, func(ref ast.Ref) bool {
		if len(ref) >= 2 && (ref[0].Value == ast.Var("data") || ref[0].Value == ast.Var("input")) && ref[1].Value == ast.String("spacelift") {
			ref[1].Value = ast.String("infraweave")
			changes++
		}
		return false
	})
	body, err := format.AstWithOpts(module, format.Opts{RegoVersion: ast.RegoV0})
	if err != nil {
		return "", 0, fmt.Errorf("catalogue: format %s: %w", name, err)
	}
	return string(body), changes, nil
}

// inputDocument adapts a structural namespace key only after the AST has proved
// this object is supplied as the root input document.
func inputDocument(term *ast.Term) int {
	object, ok := term.Value.(ast.Object)
	if !ok || object.Get(ast.StringTerm("spacelift")) == nil {
		return 0
	}
	updated := ast.NewObject()
	object.Foreach(func(key, value *ast.Term) {
		if key.Value == ast.String("spacelift") {
			key = ast.StringTerm("infraweave")
		}
		updated.Insert(key, value)
	})
	term.Value = updated
	return 1
}

func rootInput(term *ast.Term) bool {
	ref, ok := term.Value.(ast.Ref)
	return ok && len(ref) == 1 && ref[0].Value == ast.Var("input")
}

func rewriteInputDocuments(module *ast.Module) int {
	changes := 0
	ast.WalkWiths(module, func(with *ast.With) bool {
		if rootInput(with.Target) {
			changes += inputDocument(with.Value)
		}
		return false
	})
	for _, rule := range module.Rules {
		variables := map[ast.Var]bool{}
		ast.WalkWiths(rule, func(with *ast.With) bool {
			variable, ok := with.Value.Value.(ast.Var)
			if ok && rootInput(with.Target) {
				variables[variable] = true
			}
			return false
		})
		ast.WalkExprs(rule, func(expr *ast.Expr) bool {
			if !expr.IsAssignment() && !expr.IsEquality() {
				return false
			}
			variable, ok := expr.Operand(0).Value.(ast.Var)
			if ok && variables[variable] {
				changes += inputDocument(expr.Operand(1))
			}
			return false
		})
	}
	return changes
}
