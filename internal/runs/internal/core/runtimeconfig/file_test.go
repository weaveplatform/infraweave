package runtimeconfig

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestUnitRepositoryFile(t *testing.T) {
	for _, version := range []string{"", "version: '1'\n", "version: '2'\n"} {
		f := ParseFile([]byte(version+`stack_defaults:
  before_plan: [defaults]
  environment: {LEVEL: defaults, ONLY: default}
  project_root: defaults
stacks:
  infra:
    before_plan: []
    project_root: ''
module_version: [ignored, malformed]
test_defaults: false
tests: 'also ignored'
`), "infra", "terraform", "warn")
		if f.Refusal != nil || len(f.Notices) != 0 || f.Entry == nil || f.Entry.Hooks["before_plan"] == nil ||
			len(f.Entry.Hooks["before_plan"]) != 0 || *f.Entry.ProjectRoot != "" || f.Defaults.Environment[0].Value != "defaults" {
			t.Fatalf("typed file: %+v", f)
		}
		if strings.Contains(f.YAML, "stack_defaults") || strings.Contains(f.YAML, "environment") || strings.Contains(f.YAML, "module_version") {
			t.Fatalf("YAML is not the selected entry: %s", f.YAML)
		}
	}
	f := ParseFile([]byte("stack_defaults: {environment: {TOKEN: raw-secret}}\n"), "missing", "terraform", "warn")
	if f.Refusal != nil || f.Version != "1" || f.Entry != nil || len(f.Defaults.Environment) != 1 ||
		f.Defaults.Environment[0].Value != "raw-secret" || strings.Contains(f.YAML, "raw-secret") || !strings.Contains(f.YAML, "TOKEN") {
		t.Fatalf("defaults extraction/redaction: %+v", f)
	}
}

func TestUnitRepositoryUnknownProjection(t *testing.T) {
	text := `root_only: root
same: root
stack_defaults:
  defaults_only: defaults
  same: defaults
  before_plan: [defaults-known]
stacks:
  infra:
    same: selected
    own: entry
    before_plan: [entry-known]
  other:
    unrelated: never-copy
`
	f := ParseFile([]byte(text), "infra", "terraform", "warn")
	if f.Refusal != nil || len(f.Notices) != 6 {
		t.Fatalf("unknown diagnostics: %+v", f)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(f.YAML), &doc); err != nil || doc["same"] != "selected" || doc["defaults_only"] != "defaults" ||
		doc["root_only"] != "root" || strings.Contains(f.YAML, "never-copy") || strings.Contains(f.YAML, "defaults-known") {
		t.Fatalf("diagnostic precedence: %s, %v", f.YAML, err)
	}
	if len(f.Entry.Hooks["before_plan"]) != 1 || f.Entry.Hooks["before_plan"][0] != "entry-known" {
		t.Fatal("unknown projection changed typed execution")
	}
	rejected := ParseFile([]byte(text), "infra", "terraform", "reject")
	if rejected.Refusal == nil || rejected.Refusal.Reason != "unknown-field" || len(rejected.Notices) != len(f.Notices) || rejected.YAML != f.YAML {
		t.Fatalf("reject policy lost diagnostics: %+v", rejected)
	}
}

func TestUnitRepositoryNestedUnknownProjection(t *testing.T) {
	f := ParseFile([]byte(`opentofu: {root_unknown: root}
stack_defaults:
  opentofu: {version: '1.6.3', default_unknown: defaults, same: defaults}
stacks:
  infra:
    opentofu: {entry_unknown: entry, same: selected}
`), "infra", "opentofu", "warn")
	var doc map[string]map[string]any
	if err := yaml.Unmarshal([]byte(f.YAML), &doc); err != nil || f.Refusal != nil || len(f.Notices) != 5 ||
		doc["opentofu"]["same"] != "selected" || doc["opentofu"]["default_unknown"] != "defaults" ||
		doc["opentofu"]["root_unknown"] != nil || doc["opentofu"]["version"] != nil || f.Entry.OpenTofu.Version != nil {
		t.Fatalf("nested diagnostic projection changed source/typed precedence: %s; %v; %v", f.YAML, f.Refusal, err)
	}
}

func TestUnitRepositoryFileRefusals(t *testing.T) {
	for _, tc := range []struct{ text, path string }{
		{"[", FilePath}, {"{}\n---\n{}", FilePath}, {"version: 2", "version"}, {"version: '3'", "version"},
		{"stacks: []", "stacks"}, {"stack_defaults: []", "stack_defaults"},
		{"stacks: {infra: {before_plan: x}}", "stacks.infra.before_plan"},
		{"stacks: {infra: {project_root: ../out}}", "stacks.infra.project_root"},
		{"stacks: {infra: {environment: {TOKEN: true}}}", "stacks.infra.environment.TOKEN"},
		{"stack_defaults: {environment: {INFRAWEAVE_API_TOKEN: secret}}", "stack_defaults.environment"},
		{"stacks: {infra: {terragrunt: {}}}", "stacks.infra.terragrunt"},
		{"unknown: &cycle {again: *cycle}", FilePath},
	} {
		t.Run(tc.path+tc.text[:1], func(t *testing.T) {
			f := ParseFile([]byte(tc.text), "infra", "terraform", "warn")
			if f.Refusal == nil || !strings.Contains(f.Refusal.Error(), tc.path) || strings.Contains(f.Refusal.Error(), "secret") {
				t.Fatalf("refusal did not name %s safely: %+v", tc.path, f.Refusal)
			}
		})
	}
	for _, size := range []int{(1 << 20), (1 << 20) + 1} {
		f := ParseFile([]byte("#"+strings.Repeat("x", size-1)), "infra", "terraform", "warn")
		if (f.Refusal != nil) != (size > 1<<20) {
			t.Fatalf("file size %d: %+v", size, f.Refusal)
		}
	}
	text := "unknown: &value '" + strings.Repeat("x", 600000) + "'\ncopy: *value\n"
	if f := ParseFile([]byte(text), "infra", "terraform", "warn"); f.Refusal == nil || !strings.Contains(f.Refusal.Error(), "expansion") {
		t.Fatalf("unbounded expanded alias: %+v", f.Refusal)
	}
}

func TestUnitRepositoryAliasRedaction(t *testing.T) {
	const secret = "file-origin-secret"
	for _, text := range []string{
		"stack_defaults: {outside: &s " + secret + ", environment: {TOKEN: *s}}\nstacks: {infra: {unknown: *s}}",
		"stack_defaults: {environment: {OLD: &s " + secret + "}}\nstacks: {infra: {environment: {NEW: safe}, unknown: *s}}",
		"stacks: {other: {environment: {TOKEN: &s " + secret + "}}, infra: {unknown: *s}}",
		"stack_defaults: &d {environment: {TOKEN: &s " + secret + "}, unknown: *s}\nstacks: {infra: {<<: *d, before_plan: [echo]}}",
		"stack_defaults: {environment: {<<: {TOKEN: &s " + secret + "}, TOKEN: safe}}\nstacks: {infra: {unknown: *s}}",
	} {
		f := ParseFile([]byte(text), "infra", "terraform", "warn")
		if f.Refusal != nil || strings.Contains(f.YAML, secret) || strings.Contains(f.YAML, "*s") || strings.Contains(f.YAML, "&s") {
			t.Fatalf("alias redaction: %s; %v", f.YAML, f.Refusal)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(f.YAML), &doc); err != nil {
			t.Fatalf("external alias survived selection: %v", err)
		}
	}
	f := ParseFile([]byte("stack_defaults: {environment: {TOKEN: &s 'echo secret'}}\nstacks: {infra: {before_plan: [*s]}}"), "infra", "terraform", "warn")
	if f.Refusal == nil || !strings.Contains(f.Refusal.Error(), "stacks.infra.before_plan") || strings.Contains(f.YAML, "echo secret") {
		t.Fatalf("known setting accepted an environment origin: %+v", f)
	}
	f = ParseFile([]byte("stack_defaults: {environment: {TOKEN: 'identical'}}\nstacks: {infra: {unknown: identical}}"), "infra", "terraform", "warn")
	if f.Refusal != nil || !strings.Contains(f.YAML, "identical") {
		t.Fatalf("independent literal was redacted: %+v", f)
	}
}

func TestUnitRepositorySensitiveKeyOrigins(t *testing.T) {
	const secret = "file-key-origin-secret"
	for _, text := range []string{
		"&s " + secret + ": kept\nstack_defaults: {environment: {TOKEN: *s}}",
		"&s " + secret + ": kept\n" + secret + ": duplicate\nstack_defaults: {environment: {TOKEN: *s}}",
		"stack_defaults: {&s " + secret + ": kept, environment: {TOKEN: *s}}",
		"stacks: {infra: {&s " + secret + ": kept, environment: {TOKEN: *s}}}",
		"stacks: {other: {&s " + secret + ": kept}, infra: {environment: {TOKEN: *s}}}",
		"stack_defaults: {environment: {TOKEN: &s {" + secret + ": kept}}}\nstacks: {infra: *s}",
	} {
		f := ParseFile([]byte(text), "infra", "terraform", "warn")
		if f.Refusal == nil || f.Refusal.Path != FilePath || strings.Contains(f.Refusal.Error(), secret) ||
			strings.Contains(f.YAML, secret) || len(f.Notices) != 0 {
			t.Fatalf("sensitive mapping key entered diagnostics: refusal=%v notices=%v", f.Refusal, f.Notices)
		}
	}
	f := ParseFile([]byte("stacks: {&s "+secret+": {environment: {TOKEN: *s}}}"), secret, "terraform", "warn")
	if f.Refusal == nil || f.Refusal.Path != FilePath || strings.Contains(f.Refusal.Error(), secret) || len(f.Notices) != 0 {
		t.Fatalf("sensitive stack-entry key entered a path: %v", f.Refusal)
	}
}

func TestUnitRepositoryAliasedEnvironmentKey(t *testing.T) {
	const secret = "runtime-key-alias-secret"
	f := ParseFile([]byte("stack_defaults: {&env environment: {BASE: base}}\nstacks: {infra: {*env: {TOKEN: &s "+secret+"}, before_plan: [*s]}}"), "infra", "terraform", "warn")
	if f.Refusal == nil || strings.Contains(f.Refusal.Error(), secret) || strings.Contains(f.YAML, secret) {
		t.Fatalf("aliased environment key bypassed origin collection: refusal=%v, leaked=%t", f.Refusal, strings.Contains(f.YAML, secret))
	}
}
