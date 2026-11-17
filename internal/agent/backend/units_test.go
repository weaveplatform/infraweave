package backend_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/backend"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// tree writes files, each path with / separators to its content, under a
// fresh directory and answers it. A content "-> target" is a symbolic
// link to target.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		var err error
		if target, ok := strings.CutPrefix(content, "-> "); ok {
			err = os.Symlink(target, p)
		} else {
			err = os.WriteFile(p, []byte(content), 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// terragrunt is a handoff of a Terragrunt stack's run with managed state.
func terragrunt() handoff.Handoff {
	h := managed("https://relay.internal:8443")
	h.Spec.Terragrunt = &wire.RunSpecTerragrunt{RunAll: true, Version: "1.2.2", Download: true}
	return h
}

// written are the backend files under root, each by its path from root
// with / separators.
func written(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Name() != backend.File {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(b)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestUnitBackendUnits checks that a Terragrunt stack's run writes the
// backend file into each unit's directory, which the golden files under
// testdata/units hold: the unit at the project root naming the stack's
// default workspace, a nested unit its path escaped as one segment, and
// a unit in a hidden directory its own. A directory Terragrunt skips, one
// without terragrunt.hcl and a link to a unit get none, and the run token
// is in the environment alone.
func TestUnitBackendUnits(t *testing.T) {
	ws := tree(t, map[string]string{
		"root.hcl": "locals {\n  region = \"eu-west-1\"\n}\n",
		"infra/terragrunt.hcl": "include \"root\" {\n" +
			"  path = find_in_parent_folders(\"root.hcl\")\n}\n",
		"infra/main.tf": "resource \"terraform_data\" \"r\" {}\n",
		"infra/a/b/terragrunt.hcl": "include \"root\" {\n" +
			"  path = \"${get_repo_root()}/root.hcl\"\n}\n" +
			"terraform {\n  source = \"../../modules/x\"\n}\n",
		"infra/a/b/main.tf":                        "terraform {\n  required_version = \">= 1.5\"\n}\n",
		"infra/a/b/versions.tf.json":               `{"terraform": {"required_version": ">= 1.5"}}`,
		"infra/a/b/broken.tf":                      "terraform {",
		"infra/.hidden/c/terragrunt.hcl":           "",
		"infra/.terragrunt-cache/x/terragrunt.hcl": "",
		"infra/.terraform/modules/terragrunt.hcl":  "",
		"infra/.git/terragrunt.hcl":                "",
		"infra/d/main.tf":                          "",
	})
	root := filepath.Join(ws, "infra")
	if err := os.Symlink(filepath.Join(root, "a"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	units, err := backend.Units(root)
	if err != nil || !slices.Equal(units, []string{"", ".hidden/c", "a/b"}) {
		t.Fatalf("units = %q, %v", units, err)
	}
	env, err := backend.Configure(ws, root, terragrunt())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(env, []string{"TF_HTTP_USERNAME=run", "TF_HTTP_PASSWORD=" + token}) {
		t.Errorf("environment = %q", env)
	}
	got := written(t, root)
	if len(got) != 3 {
		t.Errorf("backend files %v", slices.Sorted(func(yield func(string) bool) {
			for k := range got {
				if !yield(k) {
					return
				}
			}
		}))
	}
	for _, name := range []string{backend.File, "a/b/" + backend.File} {
		golden := filepath.Join("testdata", "units", filepath.FromSlash(name))
		if *update {
			if err := os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, []byte(got[name]), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run go test -update)", err)
		}
		if got[name] != string(want) {
			t.Errorf("%s differs from %s; run go test ./internal/agent/backend -update and "+
				"review the diff:\n%s", name, golden, got[name])
		}
	}
	if c := got[".hidden/c/"+backend.File]; !strings.Contains(c,
		`"https://relay.internal:8443/state/v1/stacks/`+stack+`/workspaces/.hidden%2Fc"`) {
		t.Errorf("the hidden unit's file:\n%s", c)
	}
	for name, c := range got {
		if strings.Contains(c, "secret-part") {
			t.Errorf("%s holds the run token", name)
		}
	}

	h := terragrunt()
	h.Spec.StateBackend = nil
	bare := tree(t, map[string]string{"terragrunt.hcl": "remote_state {\n}\n"})
	if env, err := backend.Configure(bare, bare, h); env != nil || err != nil ||
		len(written(t, bare)) != 0 {
		t.Errorf("a Terragrunt stack without managed state = %q, %v", env, err)
	}
}

// TestUnitBackendSingleUnit checks only the project root's backend and
// leaves nested units and explicit stacks unvisited and unwritten.
func TestUnitBackendSingleUnit(t *testing.T) {
	ws := tree(t, map[string]string{
		"live/terragrunt.hcl":              "",
		"live/nested/terragrunt.hcl":       "remote_state {\n  backend = \"local\"\n}\n",
		"live/nested/terragrunt.stack.hcl": "unit \"generated\" {}",
	})
	root := filepath.Join(ws, "live")
	h := terragrunt()
	h.Spec.Terragrunt.RunAll = false
	if _, err := backend.Configure(ws, root, h); err != nil {
		t.Fatal(err)
	}
	got := written(t, root)
	if len(got) != 1 || !strings.Contains(got[backend.File], "/state/v1/stacks/"+stack+"\"") {
		t.Errorf("single-unit backend files = %v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "terragrunt.hcl"), []byte("remote_state {\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Configure(ws, root, h); !errors.Is(err, backend.ErrOwnBackend) {
		t.Errorf("the root unit's own backend = %v", err)
	}
}

// TestUnitBackendUnitsRefused checks that a Terragrunt project whose
// units the managed backend cannot hold is refused naming the unit and
// what it declares, writing no backend file into any unit: remote_state
// in a unit's terragrunt.hcl, or in a file it includes, which each of
// Terragrunt's functions that locate a file finds; a generate block that
// writes a backend; a backend or cloud block in a unit's Terraform file;
// a name over 512 bytes; a stack file, whose units no backend file
// reaches; and a unit's files that cannot be read.
func TestUnitBackendUnitsRefused(t *testing.T) {
	const state = "remote_state {\n  backend = \"s3\"\n}\n"
	include := func(path string) string {
		return "include \"root\" {\n  path = " + path + "\n}\n"
	}
	long := strings.Repeat("u", 200) + "/" + strings.Repeat("v", 200) + "/" +
		strings.Repeat("w", 200)
	for name, c := range map[string]struct {
		files map[string]string
		err   error
		note  string
	}{
		"a remote_state block": {
			map[string]string{"infra/a/b/terragrunt.hcl": state},
			backend.ErrOwnBackend,
			`the unit "a/b" declares its own state backend: remote_state in terragrunt.hcl`,
		},
		"a remote_state attribute": {
			map[string]string{"infra/terragrunt.hcl": "remote_state = local.state\n"},
			backend.ErrOwnBackend, "the unit at the project root declares its own state " +
				"backend: remote_state in terragrunt.hcl",
		},
		"an include found in a parent folder": {
			map[string]string{
				"root.hcl":                 state,
				"infra/a/b/terragrunt.hcl": include(`find_in_parent_folders("root.hcl")`),
			},
			backend.ErrOwnBackend, `the unit "a/b" declares its own state backend: ` +
				"remote_state in ../root.hcl, which it includes",
		},
		"an include of the parent's terragrunt.hcl": {
			map[string]string{
				"terragrunt.hcl":           state,
				"infra/a/b/terragrunt.hcl": include(`find_in_parent_folders()`),
			},
			backend.ErrOwnBackend, "remote_state in ../terragrunt.hcl, which it includes",
		},
		"an include's fallback": {
			map[string]string{
				"infra/a/fallback.hcl": state,
				"infra/a/b/terragrunt.hcl": include(
					`find_in_parent_folders("missing.hcl", "../fallback.hcl")`),
			},
			backend.ErrOwnBackend, "remote_state in a/fallback.hcl, which it includes",
		},
		"an include beside the unit": {
			map[string]string{
				"infra/a/common.hcl":       state,
				"infra/a/b/terragrunt.hcl": include(`"${get_terragrunt_dir()}/../common.hcl"`),
			},
			backend.ErrOwnBackend, "remote_state in a/common.hcl, which it includes",
		},
		"an include from the repository's root": {
			map[string]string{
				"common/state.hcl":         state,
				"infra/a/b/terragrunt.hcl": include(`"${get_path_to_repo_root()}/common/state.hcl"`),
			},
			backend.ErrOwnBackend, "remote_state in ../common/state.hcl, which it includes",
		},
		"a generated backend": {
			map[string]string{"infra/a/terragrunt.hcl": "generate \"backend\" {\n" +
				"  path     = \"backend.tf\"\n  contents = <<EOF\n" +
				"terraform {\n  backend \"s3\" {}\n}\nEOF\n}\n"},
			backend.ErrOwnBackend, `the unit "a" declares its own state backend: a generate ` +
				`block "backend" writing a backend "s3" block in terragrunt.hcl`,
		},
		"a generated cloud block": {
			map[string]string{"infra/a/terragrunt.hcl": "generate {\n" +
				"  contents = \"terraform {\\n  cloud {}\\n}\\n\"\n}\n"},
			backend.ErrOwnBackend, "a generate block writing a cloud block in terragrunt.hcl",
		},
		"a backend in a .tf file": {
			map[string]string{
				"infra/a/terragrunt.hcl": "",
				"infra/a/main.tf":        "terraform {\n  backend \"local\" {}\n}\n",
			},
			backend.ErrOwnBackend, `the unit "a" declares its own state backend: ` +
				`backend "local" block in main.tf`,
		},
		"a cloud block in a .tf file": {
			map[string]string{
				"infra/a/terragrunt.hcl": "",
				"infra/a/cloud.tf":       "terraform {\n  cloud {\n    organization = \"o\"\n  }\n}\n",
			},
			backend.ErrOwnBackend, "cloud block in cloud.tf",
		},
		"a backend in a .tf.json file": {
			map[string]string{
				"infra/a/terragrunt.hcl":  "",
				"infra/a/backend.tf.json": `{"terraform": {"backend": {"s3": {}}}}`,
			},
			backend.ErrOwnBackend, `backend "s3" block in backend.tf.json`,
		},
		"a name over 512 bytes": {
			map[string]string{"infra/" + long + "/terragrunt.hcl": ""},
			backend.ErrUnitName,
			`the unit "` + strings.Repeat("u", 64) + `"… cannot name a workspace: its name is ` +
				"602 bytes, over 512",
		},
		"a stack file": {
			map[string]string{
				"infra/a/terragrunt.hcl":       "",
				"infra/s/terragrunt.stack.hcl": "",
			},
			backend.ErrStackFile, `"s/terragrunt.stack.hcl" generates its units when the run ` +
				"runs, so no backend file reaches them",
		},
		"a stack file at the project root": {
			map[string]string{"infra/terragrunt.stack.hcl": ""},
			backend.ErrStackFile,
			`"terragrunt.stack.hcl" generates its units`,
		},
		"a terragrunt.hcl linked outside the workspace": {
			map[string]string{"infra/a/terragrunt.hcl": "-> /etc/hosts"},
			nil,
			`read the unit "a"'s terragrunt.hcl: `,
		},
		"a terragrunt.hcl that does not parse": {
			map[string]string{"infra/a/terragrunt.hcl": "include {"},
			nil,
			`read the unit "a"'s terragrunt.hcl: terragrunt.hcl:1,`,
		},
		"a missing include": {
			map[string]string{"infra/a/terragrunt.hcl": include(`"../missing.hcl"`)},
			nil,
			`read the unit "a"'s include missing.hcl: `,
		},
		"an include outside the workspace": {
			map[string]string{"infra/a/terragrunt.hcl": include(`"/etc/root.hcl"`)},
			nil,
			"is not under the workspace",
		},
		"an include that is a directory": {
			map[string]string{"infra/a/terragrunt.hcl": include(`get_terragrunt_dir()`)},
			nil,
			`read the unit "a"'s include a: `,
		},
		"an include that does not parse": {
			map[string]string{
				"infra/root.hcl":         "locals {",
				"infra/a/terragrunt.hcl": include(`find_in_parent_folders("root.hcl")`),
			},
			nil, `read the unit "a"'s include root.hcl: root.hcl:1,`,
		},
		"a terragrunt.hcl over 1 MiB": {
			map[string]string{"infra/a/terragrunt.hcl": strings.Repeat("#", 1<<20+1)},
			nil,
			"it is over 1 MiB",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ws := tree(t, c.files)
			root := filepath.Join(ws, "infra")
			_, err := backend.Configure(ws, root, terragrunt())
			if err == nil || c.err != nil && !errors.Is(err, c.err) ||
				!strings.Contains(err.Error(), c.note) {
				t.Fatalf("got %v, want %v with %q", err, c.err, c.note)
			}
			if got := written(t, ws); len(got) != 0 {
				t.Errorf("a refused project has backend files %v", got)
			}
		})
	}
}

// TestUnitBackendUnitsUnread checks what the refusals leave to the tool:
// an include whose path the check cannot work out or finds no file for,
// a Terraform file that does not parse, and a generate block whose
// contents are not a string the check reads.
func TestUnitBackendUnitsUnread(t *testing.T) {
	ws := tree(t, map[string]string{
		"infra/a/terragrunt.hcl": "include \"x\" {\n  path = local.path\n}\n" +
			"include \"y\" {\n  path = find_in_parent_folders(\"none.hcl\")\n}\n" +
			"include \"z\" {\n  expose = true\n}\n" +
			"generate \"g\" {\n  contents = local.contents\n}\n" +
			"generate \"h\" {\n  path = \"x.tf\"\n}\n" +
			"generate \"i\" {\n  contents = \"terraform {\"\n}\n",
		"infra/a/broken.tf":      "terraform {\n  backend \"s3\" {\n",
		"infra/a/broken.tf.json": "{",
		"infra/a/README.md":      "terraform { backend \"s3\" {} }",
		"infra/a/outside.tf":     "-> /etc/hosts",
		"infra/a/sub/main.tf":    "terraform {\n  backend \"s3\" {}\n}\n",
	})
	root := filepath.Join(ws, "infra")
	if _, err := backend.Configure(ws, root, terragrunt()); err != nil {
		t.Fatal(err)
	}
	if got := written(t, root); len(got) != 1 || got["a/"+backend.File] == "" {
		t.Errorf("backend files %v", got)
	}
}

// TestUnitBackendUnitsRelative checks that a workspace and a project
// root relative to the working directory, as the runner's are, find a
// unit's includes as absolute ones do, and that an absolute include
// outside them is refused.
func TestUnitBackendUnitsRelative(t *testing.T) {
	ws := tree(t, map[string]string{
		"root.hcl":               "remote_state {\n}\n",
		"infra/a/terragrunt.hcl": "include {\n  path = find_in_parent_folders(\"root.hcl\")\n}\n",
		"infra/b/terragrunt.hcl": "include {\n  path = \"/etc/root.hcl\"\n}\n",
	})
	t.Chdir(ws)
	if _, err := backend.Configure(".", "infra", terragrunt()); !errors.Is(err,
		backend.ErrOwnBackend) || !strings.Contains(err.Error(), "remote_state in ../root.hcl") {
		t.Errorf("a relative include: %v", err)
	}
	if err := os.WriteFile("root.hcl", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Configure(".", "infra", terragrunt()); err == nil ||
		!strings.Contains(err.Error(), "/etc/root.hcl is not under the workspace") ||
		!strings.HasPrefix(err.Error(), `read the unit "b"'s include ../`) {
		t.Errorf("an absolute include: %v", err)
	}
}

// TestUnitBackendUnitsUnreadable checks that a project whose directories
// cannot be read, or whose workspace is not a directory, is refused, and
// that a unit's directory that cannot hold the backend file names the
// unit.
func TestUnitBackendUnitsUnreadable(t *testing.T) {
	if _, err := backend.Units(filepath.Join(t.TempDir(), "missing")); err == nil ||
		!strings.HasPrefix(err.Error(), "find the Terragrunt units: ") {
		t.Errorf("a missing project root: %v", err)
	}
	ws := tree(t, map[string]string{"infra/a/terragrunt.hcl": ""})
	root := filepath.Join(ws, "infra")
	if _, err := backend.Configure(filepath.Join(ws, "missing"), root,
		terragrunt()); err == nil || !strings.Contains(err.Error(), "open the workspace: ") {
		t.Errorf("a missing workspace: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "a", backend.File, "x"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Configure(ws, root, terragrunt()); err == nil ||
		!strings.HasPrefix(err.Error(), `the unit "a": replace `+backend.File) {
		t.Errorf("a unit that cannot hold the file: %v", err)
	}
	h := terragrunt()
	h.Endpoint = "ftp://relay"
	if _, err := backend.Configure(ws, root, h); !errors.Is(err, backend.ErrAddress) {
		t.Errorf("an FTP endpoint: %v", err)
	}
}
