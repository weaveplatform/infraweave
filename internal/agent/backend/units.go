package backend

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	hcljson "github.com/hashicorp/hcl/v2/json"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// The names Terragrunt gives its files: a unit's configuration, which
// makes its directory a unit, the configuration a unit includes when it
// names none, and a stack's file, whose units Terragrunt generates when
// the run runs.
const (
	UnitFile  = "terragrunt.hcl"
	StackFile = "terragrunt.stack.hcl"
)

// MaxWorkspaceName is the longest workspace name, in bytes, the state
// backend takes; it is internal/state's core.MaxWorkspaceName, which the
// agent cannot import.
const MaxWorkspaceName = 512

// maxConfig bounds each file a unit's check reads, 1 MiB as errTooLarge
// says; a Terragrunt or Terraform file is a few KiB.
const maxConfig = 1 << 20

// Errors of a file a unit's check does not read.
var (
	errOutside  = errors.New("is not under the workspace")
	errTooLarge = errors.New("it is over 1 MiB")
)

// shownName is the most of a unit's name a note shows: a note is at most
// 1024 bytes, the worker API's bound on a report's details, and a unit's
// name can be as long as a path.
const shownName = 64

// Errors of a Terragrunt project whose units the managed state backend
// cannot hold.
var (
	ErrOwnBackend = errors.New("declares its own state backend")
	ErrUnitName   = errors.New("cannot name a workspace")
	ErrStackFile  = errors.New("generates its units when the run runs, so no backend file " +
		"reaches them")
)

// skipped are the directories neither Terragrunt's discovery nor Units
// enters: the repository's, a provider cache, and Terragrunt's copies of
// each unit, which hold the unit's files again.
var skipped = []string{".git", ".terraform", ".terragrunt-cache"}

// Units are the Terragrunt units under root, the run's project root:
// each directory with a UnitFile, found as terragrunt run --all finds
// them, never through a symbolic link. Each is named by its path from
// root with / separators, empty for root itself, the name of its state
// workspace, in order of their names. A StackFile under root is
// ErrStackFile naming it.
//
// ref: decision 0010 § "Endpoints"
func Units(root string) ([]string, error) {
	var units []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && p != root && slices.Contains(skipped, d.Name()):
			return filepath.SkipDir
		case d.IsDir():
			return nil
		}
		// p is under root, which the walk starts from.
		rel, _ := filepath.Rel(root, filepath.Dir(p))
		name := filepath.ToSlash(rel)
		if name == "." {
			name = ""
		}
		switch d.Name() {
		case UnitFile:
			units = append(units, name)
		case StackFile:
			return fmt.Errorf("%s %w", strconv.Quote(inUnit(name, StackFile)), ErrStackFile)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find the Terragrunt units: %w", err)
	}
	slices.Sort(units)
	return units, nil
}

// inUnit is file in the directory name names, from the project root.
func inUnit(name, file string) string {
	if name == "" {
		return file
	}
	return name + "/" + file
}

// unit is how a note names the unit name, at most shownName bytes of it.
func unit(name string) string {
	if name == "" {
		return "the unit at the project root"
	}
	if len(name) > shownName {
		return "the unit " + strconv.Quote(name[:shownName]) + "…"
	}
	return "the unit " + strconv.Quote(name)
}

// checkName refuses a unit whose name the state backend does not take
// as a workspace name.
func checkName(name string) error {
	switch {
	case len(name) > MaxWorkspaceName:
		return fmt.Errorf("%s %w: its name is %d bytes, over %d", unit(name), ErrUnitName,
			len(name), MaxWorkspaceName)
	case !utf8.ValidString(name):
		return fmt.Errorf("%s %w: its name is not UTF-8", unit(name), ErrUnitName)
	}
	return nil
}

// project reads a Terragrunt project's files under workspace, opened as a
// root, so no link the repository commits reads outside it.
type project struct {
	fs        *os.Root
	workspace string
	root      string
}

// checkBackend refuses the unit name of the project when it declares its
// own state backend: remote_state in its UnitFile or in a file it
// includes, a generate block whose contents declare a backend, or a
// backend or cloud block in a Terraform file of its directory. An
// include whose path the check cannot work out, or a module that a
// unit's terraform block sources, is not read; the tool's initialization
// refuses the second backend such a file declares.
//
// ref: decision 0010 § "How a run reaches it"
func (p *project) checkBackend(name string) error {
	dir := filepath.Join(p.root, filepath.FromSlash(name))
	file := filepath.Join(dir, UnitFile)
	body, err := p.parse(file)
	if err != nil {
		return fmt.Errorf("read %s's %s: %w", unit(name), UnitFile, err)
	}
	if what := ownState(body); what != "" {
		return fmt.Errorf("%s %w: %s in %s", unit(name), ErrOwnBackend, what, UnitFile)
	}
	for _, b := range body.Blocks {
		if b.Type != "include" {
			continue
		}
		included, ok := p.include(dir, b.Body)
		if !ok {
			continue
		}
		inc, err := p.parse(included)
		if err != nil {
			return fmt.Errorf("read %s's include %s: %w", unit(name), p.show(included), err)
		}
		if what := ownState(inc); what != "" {
			return fmt.Errorf("%s %w: %s in %s, which it includes", unit(name), ErrOwnBackend,
				what, p.show(included))
		}
	}
	// Units read the directory a moment ago; should it fail now, the
	// tool reads no file of it either.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || e.Name() == File {
			continue
		}
		if what := p.declares(filepath.Join(dir, e.Name())); what != "" {
			return fmt.Errorf("%s %w: %s in %s", unit(name), ErrOwnBackend, what, e.Name())
		}
	}
	return nil
}

// ownState is what in a Terragrunt file's body declares a state backend:
// remote_state, as an attribute or a block, or a generate block whose
// contents declare a backend; empty for nothing.
func ownState(body *hclsyntax.Body) string {
	if _, ok := body.Attributes["remote_state"]; ok {
		return "remote_state"
	}
	for _, b := range body.Blocks {
		switch b.Type {
		case "remote_state":
			return "remote_state"
		case "generate":
			contents, ok := b.Body.Attributes["contents"]
			if !ok {
				continue
			}
			v, diags := contents.Expr.Value(nil)
			if diags.HasErrors() || !v.Type().Equals(cty.String) || !v.IsKnown() || v.IsNull() {
				continue
			}
			f, diags := hclsyntax.ParseConfig([]byte(v.AsString()), "generate", hcl.InitialPos)
			if diags.HasErrors() {
				continue
			}
			if what := backendIn(f.Body); what != "" {
				label := ""
				if len(b.Labels) > 0 {
					label = " " + strconv.Quote(b.Labels[0])
				}
				return "a generate block" + label + " writing a " + what
			}
		}
	}
	return ""
}

// declares is what the Terraform file at name declares of a backend: a
// backend or a cloud block in a terraform block, of HCL in a .tf file or
// JSON in a .tf.json one; empty for neither, or for a file of another
// kind or that does not parse, which the tool reports.
func (p *project) declares(name string) string {
	var parse func([]byte, string) (*hcl.File, hcl.Diagnostics)
	switch {
	case strings.HasSuffix(name, ".tf"):
		parse = func(b []byte, n string) (*hcl.File, hcl.Diagnostics) {
			return hclsyntax.ParseConfig(b, n, hcl.InitialPos)
		}
	case strings.HasSuffix(name, ".tf.json"):
		parse = hcljson.Parse
	default:
		return ""
	}
	data, err := p.read(name)
	if err != nil {
		return ""
	}
	f, diags := parse(data, filepath.Base(name))
	if diags.HasErrors() {
		return ""
	}
	return backendIn(f.Body)
}

// backendIn is the first backend or cloud block in a terraform block of
// body, as a note names it; empty for none.
func backendIn(body hcl.Body) string {
	top, _, _ := body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "terraform"}},
	})
	for _, t := range top.Blocks {
		in, _, _ := t.Body.PartialContent(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{
			{Type: "backend", LabelNames: []string{"type"}}, {Type: "cloud"},
		}})
		for _, b := range in.Blocks {
			if b.Type == "cloud" {
				return "cloud block"
			}
			return "backend " + strconv.Quote(b.Labels[0]) + " block"
		}
	}
	return ""
}

// parse reads the Terragrunt file at name, an absolute path under the
// workspace.
func (p *project) parse(name string) (*hclsyntax.Body, error) {
	data, err := p.read(name)
	if err != nil {
		return nil, err
	}
	f, diags := hclsyntax.ParseConfig(data, filepath.Base(name), hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%w", diags)
	}
	// hclsyntax's every file has its own kind of body.
	return f.Body.(*hclsyntax.Body), nil
}

// read reads at most maxConfig bytes of the file at name, an absolute
// path, through the workspace's root.
func (p *project) read(name string) ([]byte, error) {
	rel, err := filepath.Rel(p.workspace, name)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("%s %w", p.show(name), errOutside)
	}
	f, err := p.fs.Open(rel)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller names the file
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfig+1))
	switch {
	case err != nil:
		return nil, err //nolint:wrapcheck // as above
	case len(data) > maxConfig:
		return nil, errTooLarge
	}
	return data, nil
}

// show is the absolute path name, under the workspace or not, as a note
// shows it: from the project root with / separators.
func (p *project) show(name string) string {
	rel, _ := filepath.Rel(p.root, name) // both are absolute
	return filepath.ToSlash(rel)
}

// include is the absolute path of the file an include block's body in
// the unit directory dir names, false when its path is no string the
// check works out: a literal, a template, or a call of the Terragrunt
// functions that locate files, which find_in_parent_folders answers by
// searching no higher than the workspace.
//
// ref: https://docs.terragrunt.com/reference/hcl/functions/
func (p *project) include(dir string, body *hclsyntax.Body) (string, bool) {
	attr, ok := body.Attributes["path"]
	if !ok {
		return "", false
	}
	// dir is a unit's directory, under the workspace.
	toRoot, _ := filepath.Rel(dir, p.workspace)
	ctx := &hcl.EvalContext{Functions: map[string]function.Function{
		"find_in_parent_folders": function.New(&function.Spec{
			VarParam: &function.Parameter{Type: cty.String},
			Type:     function.StaticReturnType(cty.String),
			Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
				found, err := p.findInParents(dir, args)
				return cty.StringVal(found), err
			},
		}),
		"get_terragrunt_dir":    constant(dir),
		"get_repo_root":         constant(p.workspace),
		"get_path_to_repo_root": constant(filepath.ToSlash(toRoot)),
	}}
	v, diags := attr.Expr.Value(ctx)
	if diags.HasErrors() || !v.Type().Equals(cty.String) || !v.IsKnown() || v.IsNull() {
		return "", false
	}
	name := filepath.FromSlash(v.AsString())
	if !filepath.IsAbs(name) {
		name = filepath.Join(dir, name)
	}
	return filepath.Clean(name), true
}

// constant is a function of no arguments that answers s.
func constant(s string) function.Function {
	return function.New(&function.Spec{
		Type: function.StaticReturnType(cty.String),
		Impl: func([]cty.Value, cty.Type) (cty.Value, error) { return cty.StringVal(s), nil },
	})
}

// errNotFound is a find_in_parent_folders call that finds nothing and
// names no fallback.
var errNotFound = errors.New("no parent folder holds the file")

// findInParents answers find_in_parent_folders(args...) of a unit in
// dir: the first of dir's parents, no higher than the workspace, holding
// the file args name, UnitFile when they name none, else the fallback
// the second argument names.
func (p *project) findInParents(dir string, args []cty.Value) (string, error) {
	file := UnitFile
	if len(args) > 0 && args[0].IsKnown() && !args[0].IsNull() {
		file = args[0].AsString()
	}
	for d := filepath.Dir(dir); ; d = filepath.Dir(d) {
		rel, err := filepath.Rel(p.workspace, filepath.Join(d, file))
		if err == nil && filepath.IsLocal(rel) {
			if _, err := p.fs.Stat(rel); err == nil {
				return filepath.Join(d, file), nil
			}
		}
		if d == p.workspace || d == filepath.Dir(d) {
			break
		}
	}
	if len(args) > 1 && args[1].IsKnown() && !args[1].IsNull() {
		return args[1].AsString(), nil
	}
	return "", errNotFound
}
