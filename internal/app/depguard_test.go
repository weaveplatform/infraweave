package app

import (
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"
)

const module = "github.com/weaveplatform/infraweave"

// sharedRules are the cross-cutting depguard rules, 3 to 10 in
// repo-layout.md's "depguard rules"; the per-domain graph rules make up the
// rest. make lint-fixtures proves each one fires.
//
// ref: decision 0004
var sharedRules = []string{
	"api-packages", "constructors", "core-not-adapters", "shared-below-domains",
	"agent-isolation", "devtools", "public-packages", "ctl",
}

// notDomains are the directories under internal/ that sit beside the domains.
var notDomains = []string{"agent", "app", "ctl", "devtools", "iac", "kernel", "platform"}

type depguardRule struct {
	ListMode string         `yaml:"list-mode"` //nolint:tagliatelle // depguard's own key
	Files    []string       `yaml:"files"`
	Allow    []string       `yaml:"allow"`
	Deny     []depguardDeny `yaml:"deny"`
}

type depguardDeny struct {
	Pkg  string `yaml:"pkg"`
	Desc string `yaml:"desc"`
}

func (r depguardRule) denies(pkg string) bool {
	return slices.ContainsFunc(r.Deny, func(d depguardDeny) bool { return d.Pkg == pkg })
}

func readDepguardRules(t *testing.T, path string) map[string]depguardRule {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Linters struct {
			Settings struct {
				Depguard struct {
					Rules map[string]depguardRule `yaml:"rules"`
				} `yaml:"depguard"`
			} `yaml:"settings"`
		} `yaml:"linters"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.Linters.Settings.Depguard.Rules
}

// depguardProblems holds rules to the conventions depguard cannot check
// itself: entries match as plain string prefixes, so an allow entry without
// a trailing $ or / admits its siblings (outbox would admit outboxutil) and a
// deny entry with one refuses less than it says; and each domain directory
// under internal (an fs.FS rooted there) needs its graph rule and its entries
// in the shared rules, or its imports go unchecked.
func depguardProblems(rules map[string]depguardRule, internal fs.FS) ([]string, error) {
	var out []string
	for _, name := range sharedRules {
		if _, ok := rules[name]; !ok {
			out = append(out, "no "+name+" rule")
		}
	}
	for _, name := range slices.Sorted(maps.Keys(rules)) {
		for _, a := range rules[name].Allow {
			if !strings.HasPrefix(a, "$") && !strings.HasSuffix(a, "$") &&
				!strings.HasSuffix(a, "/") {
				out = append(out, name+": allow entry "+a+" ends in neither $ nor /")
			}
		}
		for _, d := range rules[name].Deny {
			if strings.HasSuffix(d.Pkg, "$") || strings.HasSuffix(d.Pkg, "/") {
				out = append(out, name+": deny entry "+d.Pkg+" is not bare")
			}
		}
	}
	entries, err := fs.ReadDir(internal, ".")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		d := e.Name()
		if !e.IsDir() || slices.Contains(notDomains, d) {
			continue
		}
		root := module + "/internal/" + d
		if _, ok := rules["graph-"+d]; !ok {
			out = append(out, d+": no graph-"+d+" rule")
		}
		if !rules["constructors"].denies(root + "/assembly") {
			out = append(out, d+": constructors does not deny "+root+"/assembly")
		}
		if !rules["shared-below-domains"].denies(root) {
			out = append(out, d+": shared-below-domains does not deny "+root)
		}
		if _, err := fs.Stat(internal, d+"/internal/core"); err == nil &&
			!rules["core-not-adapters"].denies(root+"/internal/adapters") {
			out = append(out, d+": core-not-adapters does not deny "+root+"/internal/adapters")
		}
	}
	return out, nil
}

func TestUnitDepguardFixtures(t *testing.T) {
	rules := readDepguardRules(t, "../../.golangci.yml")
	got, err := depguardProblems(rules, os.DirFS(".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf(".golangci.yml:\n%s", strings.Join(got, "\n"))
	}

	// A graph rule for each domain the cases use, so that only the change under
	// test is wrong.
	withGraphs := func() map[string]depguardRule {
		r := maps.Clone(rules)
		for _, d := range []string{"stacks", "runs"} {
			r["graph-"+d] = depguardRule{Files: []string{"**/internal/" + d + "/**"}}
		}
		return r
	}
	stacks := fstest.MapFS{"stacks/api/api.go": {}, "stacks/assembly/assembly.go": {}}
	runs := fstest.MapFS{"runs/internal/core/core.go": {}, "runs/internal/adapters/pg/pg.go": {}}
	for name, tc := range map[string]struct {
		tree   fstest.MapFS
		mutate func(map[string]depguardRule)
		want   string
	}{
		"flat domain": {tree: stacks},
		"hexagon":     {tree: runs},
		"bare allow entry": {
			tree: stacks,
			mutate: func(r map[string]depguardRule) {
				a := r["api-packages"]
				a.Allow = append(slices.Clone(a.Allow), module+"/internal/platform/outbox")
				r["api-packages"] = a
			},
			want: "api-packages: allow entry " + module + "/internal/platform/outbox ends in neither $ nor /",
		},
		"anchored deny entry": {
			tree: stacks,
			mutate: func(r map[string]depguardRule) {
				p := r["public-packages"]
				p.Deny = slices.Clone(p.Deny)
				p.Deny[0].Pkg += "/"
				r["public-packages"] = p
			},
			want: "public-packages: deny entry " + module + "/internal/ is not bare",
		},
		"missing shared rule": {
			tree:   stacks,
			mutate: func(r map[string]depguardRule) { delete(r, "devtools") },
			want:   "no devtools rule",
		},
		"no graph rule": {
			tree:   stacks,
			mutate: func(r map[string]depguardRule) { delete(r, "graph-stacks") },
			want:   "stacks: no graph-stacks rule",
		},
		"unlisted domain": {
			tree: fstest.MapFS{"alpha/api/api.go": {}},
			mutate: func(r map[string]depguardRule) {
				r["graph-alpha"] = depguardRule{}
			},
			want: "alpha: constructors does not deny " + module + "/internal/alpha/assembly\n" +
				"alpha: shared-below-domains does not deny " + module + "/internal/alpha",
		},
		"hexagon without core-not-adapters": {
			tree: runs,
			mutate: func(r map[string]depguardRule) {
				c := r["core-not-adapters"]
				c.Deny = slices.DeleteFunc(slices.Clone(c.Deny), func(d depguardDeny) bool {
					return strings.Contains(d.Pkg, "/runs/")
				})
				r["core-not-adapters"] = c
			},
			want: "runs: core-not-adapters does not deny " + module + "/internal/runs/internal/adapters",
		},
		"shared directories": {tree: fstest.MapFS{"platform/x/x.go": {}, "kernel/k.go": {}, "doc.go": {}}},
	} {
		t.Run(name, func(t *testing.T) {
			r := withGraphs()
			if tc.mutate != nil {
				tc.mutate(r)
			}
			got, err := depguardProblems(r, tc.tree)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "\n") != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), tc.want)
			}
		})
	}

	if _, err := depguardProblems(rules, os.DirFS(t.TempDir()+"/missing")); err == nil {
		t.Error("an unreadable internal/ must fail")
	}
}
