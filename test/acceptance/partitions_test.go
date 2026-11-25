package acceptance

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gherkin "github.com/cucumber/gherkin/go/v42"
	"github.com/cucumber/godog"
	messages "github.com/cucumber/messages/go/v34"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// scenarioTags parses every feature under dir and returns each scenario's
// tags, its feature's included, keyed by "path: scenario", the path below dir.
func scenarioTags(t *testing.T, dir string) map[string][]string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if strings.HasSuffix(p, ".feature") {
			paths = append(paths, p)
		}
		return err
	})
	if err != nil || len(paths) == 0 {
		t.Fatalf("no features in %s: %v", dir, err)
	}
	out := map[string][]string{}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := gherkin.ParseGherkinDocument(f, (&messages.Incrementing{}).NewId)
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		for _, pk := range gherkin.Pickles(*doc, p, (&messages.Incrementing{}).NewId) {
			var tags []string
			for _, tg := range pk.Tags {
				tags = append(tags, tg.Name)
			}
			rel, _ := filepath.Rel(dir, p)
			out[filepath.ToSlash(rel)+": "+pk.Name] = tags
		}
	}
	return out
}

// TestUnitFeaturePartitions holds every scenario of features/ to the
// Partitions: a tag no partition lists would leave its scenario in no CI
// job, or in one without its dependency.
//
// ref: decision 0016
func TestUnitFeaturePartitions(t *testing.T) {
	for name, tags := range scenarioTags(t, "features") {
		if _, err := harness.Place(tags); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	unlisted := 0
	for name, tags := range scenarioTags(t, filepath.Join("testdata", "partitions")) {
		if _, err := harness.Place(tags); errors.Is(err, harness.ErrUnlistedTag) {
			unlisted++
		} else if err != nil || name != "unlisted.feature: A listed scenario" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if unlisted != 1 {
		t.Errorf("the fixture's unlisted tag fails %d scenarios, want 1", unlisted)
	}
}

func TestUnitPlace(t *testing.T) {
	for _, tc := range []struct {
		tags []string
		want string
	}{
		{nil, "core"},
		{[]string{"@identity", "@postgres"}, "identity"},
		{[]string{"@identity", "@oidc"}, "identity"},
		{[]string{"@identity", "@terraform"}, "terraform"},
		{[]string{"@identity", "@vcs"}, "integrations"},
		{[]string{"@terraform-plans", "@plan-sanitization", "@terraform", "@docker"}, "terraform-plans"},
		{[]string{"@terraform-vcs", "@terraform", "@github", "@docker"}, "terraform-vcs"},
		{[]string{"@plan-sanitization", "@postgres"}, "core"},
		{[]string{"@policy-check", "@postgres"}, "core"},
		{[]string{"@plan-sanitization", "@terraform", "@docker"}, "terraform"},
		{[]string{"@plan-sanitization", "@tofu", "@docker"}, "runs"},
		{[]string{"@run-credentials", "@postgres", "@vcs", "@docker", "@tofu", "@oidc"}, "run-credentials"},
		{[]string{"@run-credentials", "@terraform", "@docker"}, "terraform"},
		{[]string{"@run-credentials", "@vcs-lab"}, "vcs-lab"},
		{[]string{"@plan-sanitization", "@vcs"}, "integrations"},
		{[]string{"@op:stacks", "@case:denial", "@quarantine"}, "core"},
		{[]string{"@postgres"}, "core"},
		{[]string{"@oidc", "@objectstore"}, "core"},
		{[]string{"@postgres", "@vault"}, "core"},
		{[]string{"@postgres", "@terraform"}, "terraform"},
		{[]string{"@runtime-config-reject", "@terraform"}, "terraform"},
		{[]string{"@terraform", "@docker", "@tofu"}, "terraform"},
		{[]string{"@runtime-config-reject", "@tofu"}, "runs"},
		{[]string{"@vcs", "@postgres"}, "integrations"},
		{[]string{"@webhook-controls", "@gitlab", "@postgres"}, "integrations"},
		{[]string{"@webhook-controls", "@postgres"}, "core"},
		{[]string{"@vcs", "@tofu"}, "runs"},
		{[]string{"@postgres", "@vcs", "@terragrunt"}, "runs"},
		{[]string{"@postgres", "@vcs", "@docker"}, "runs"},
		{[]string{"@ai"}, "integrations"},
		{[]string{"@provider:resource:spacelift_stack", "@terraform"}, "terraform"},
		{[]string{"@postgres", "@vcs", "@vcs-lab", "@lab:gitlab"}, "vcs-lab"},
		{[]string{"@postgres", "@kubernetes", "@kubernetes-lab"}, "kubernetes-lab"},
		{[]string{"@terraform", "@kubernetes-lab"}, "kubernetes-lab"},
		{[]string{"@vcs-lab", "@kubernetes-lab"}, "vcs-lab"},
		{[]string{"@postgres", "@managed-cloud-workers"}, "managed-cloud-workers"},
		{[]string{"@kubernetes-lab", "@managed-cloud-workers"}, "kubernetes-lab"},
	} {
		got, err := harness.Place(tc.tags)
		if err != nil || got != tc.want {
			t.Errorf("Place(%q) = %q, %v; want %q", tc.tags, got, err, tc.want)
		}
	}
	if _, err := harness.Place(
		[]string{"@postgres", "@redis"},
	); !errors.Is(
		err,
		harness.ErrUnlistedTag,
	) {
		t.Errorf("an unlisted tag placed: %v", err)
	}
}

func TestUnitTagExpression(t *testing.T) {
	ordinary := "~@vcs-lab && ~@kubernetes-lab && ~@managed-cloud-workers"
	credentialPrefix := ordinary + " && ~@terraform-plans && ~@terraform-vcs && ~@terraform && "
	runPrefix := credentialPrefix + "~@run-credentials && "
	integrationPrefix := runPrefix + "~@docker && ~@tofu && ~@terragrunt && ~@kubernetes && ~@flows-agent && "
	corePrefix := integrationPrefix + "~@vcs && ~@github && ~@gitlab && ~@bitbucket-cloud && ~@bitbucket-dc && ~@azure-devops && ~@ai"
	for name, want := range map[string]string{
		"":                      ordinary,
		"vcs-lab":               "@vcs-lab",
		"kubernetes-lab":        "~@vcs-lab && @kubernetes-lab",
		"managed-cloud-workers": "~@vcs-lab && ~@kubernetes-lab && @managed-cloud-workers",
		"terraform-plans":       ordinary + " && @terraform-plans",
		"terraform-vcs":         ordinary + " && ~@terraform-plans && @terraform-vcs",
		"terraform":             ordinary + " && ~@terraform-plans && ~@terraform-vcs && @terraform",
		"run-credentials":       credentialPrefix + "@run-credentials",
		"runs":                  strings.ReplaceAll("@docker,@tofu,@terragrunt,@kubernetes,@flows-agent", "@", runPrefix+"@"),
		"integrations":          strings.ReplaceAll("@vcs,@github,@gitlab,@bitbucket-cloud,@bitbucket-dc,@azure-devops,@ai", "@", integrationPrefix+"@"),
		"identity":              corePrefix + " && @identity",
		"core":                  corePrefix + " && ~@identity",
	} {
		got, err := harness.TagExpression(name)
		if err != nil || got != want {
			t.Errorf("TagExpression(%q) = %q, %v; want %q", name, got, err, want)
		}
	}

	if _, err := harness.TagExpression("everything"); !errors.Is(err, harness.ErrUnknownPartition) {
		t.Errorf("an unknown partition: %v", err)
	}
	for want, exprs := range map[string][]string{
		"":                                    {"", ""},
		"@vcs-lab":                            {"@vcs-lab", ""},
		"@vcs-lab && @lab:gitlab":             {"@vcs-lab", "", "@lab:gitlab"},
		"@a && @c,@a && @d,@b && @c,@b && @d": {"@a,@b", "@c,@d"},
		"~@a && @b,~@a && @c":                 {"~@a", "@b,@c"},
	} {
		if got := harness.And(exprs...); got != want {
			t.Errorf("And(%q) = %q, want %q", exprs, got, want)
		}
	}
}

// Godog's filter reads only ",", "&&" and "~"; Cucumber's operators are
// distributed into that form, with every negation on a tag.
//
// ref: decision 0016
func TestUnitGodogTags(t *testing.T) {
	for expr, want := range map[string]string{
		"@lab:github":                       "@lab:github",
		"@op:a or @op:b":                    "@op:a,@op:b",
		"@op:a and not @op:b":               "@op:a && ~@op:b",
		"not (@op:a or @op:b)":              "~@op:a && ~@op:b",
		"not (@op:a and @op:b)":             "~@op:a,~@op:b",
		"not not @op:a":                     "@op:a",
		"(@a or @b) and (@c or @d)":         "@a && @c,@a && @d,@b && @c,@b && @d",
		"@a or @b and @c":                   "@a,@b && @c",
		"(@vcs-lab) and (@lab:gitlab)":      "@vcs-lab && @lab:gitlab",
		"@a,@b":                             "@a,@b",
		"~@a&&@b, @c":                       "~@a && @b,@c",
		"  @op:a   or\t@op:b  ":             "@op:a,@op:b",
		"not (@a and (@b or not @c)) or @d": "~@a,~@b && @c,@d",
	} {
		if got, err := harness.GodogTags(expr); err != nil || got != want {
			t.Errorf("GodogTags(%q) = %q, %v; want %q", expr, got, err, want)
		}
	}
	for _, expr := range []string{
		"", "@a or", "@a and and @b", "op:a", "@", "(@a or @b", "@a)", "@a @b", "not", "@a,,@b", "@a &&",
	} {
		if got, err := harness.GodogTags(expr); !errors.Is(err, harness.ErrTagExpression) {
			t.Errorf("GodogTags(%q) = %q, %v; want %v", expr, got, err, harness.ErrTagExpression)
		}
	}
}

// A focus that selects nothing fails the run before it starts, naming the
// expression; the selected scenarios are those godog's own filter picks.
//
// ref: decision 0016
func TestUnitSelection(t *testing.T) {
	dir := filepath.Join("testdata", "selection")
	first, second, both := "The first operation's", "The second operation's", "Both operations'"
	for _, tc := range []struct {
		partition, focus string
		want             []string
	}{
		{"core", "@op:first or @op:second", []string{first, second, both}},
		{"core", "@op:first,@op:second", []string{first, second, both}},
		{"core", "@op:first and not @op:second", []string{first}},
		{"core", "~@op:first && @op:second", []string{second}},
		{"core", "not (@op:first or @op:second)", []string{"Neither operation's"}},
		{"vcs-lab", "@op:first", []string{"A lab's first operation's"}},
		{"core", "", []string{first, second, both, "Neither operation's"}},
	} {
		expr, err := harness.Selection(dir, tc.partition, tc.focus)
		if err != nil {
			t.Errorf("Selection(%q, %q): %v", tc.partition, tc.focus, err)
			continue
		}
		suite := godog.TestSuite{Options: &godog.Options{Paths: []string{dir}, Tags: expr}}
		features, err := suite.RetrieveFeatures()
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, f := range features {
			for _, p := range f.Pickles {
				got = append(got, p.Name)
			}
		}
		slices.Sort(got)
		slices.Sort(tc.want)
		if !slices.Equal(got, tc.want) {
			t.Errorf("Selection(%q, %q) = %q selects %q, want %q", tc.partition, tc.focus, expr, got, tc.want)
		}
	}

	_, err := harness.Selection(dir, "core", "@no-such-tag")
	if !errors.Is(err, harness.ErrNoScenario) || !strings.Contains(err.Error(), `INFRAWEAVE_ACCEPT_TAGS="@no-such-tag"`) {
		t.Errorf("a focus that selects nothing: %v", err)
	}
	if _, err := harness.Selection(dir, "core", "@op:first or"); !errors.Is(err, harness.ErrTagExpression) {
		t.Errorf("a focus that does not parse: %v", err)
	}
	if _, err := harness.Selection(dir, "everything", "@op:first"); !errors.Is(err, harness.ErrUnknownPartition) {
		t.Errorf("an unknown partition: %v", err)
	}
	if _, err := harness.Selection(filepath.Join(dir, "absent"), "core", "@op:first"); err == nil {
		t.Error("features that do not exist selected scenarios")
	}
}

func TestUnitDockerTags(t *testing.T) {
	got := harness.DockerTags([]string{
		"@postgres", "@identity", "@run-credentials", "@oidc", "@ai", "@op:stacks", "@vcs", "@runtime-config-reject", "@webhook-controls",
	})
	if !slices.Equal(got, []string{"@postgres", "@oidc", "@vcs"}) {
		t.Errorf("dockerTags = %q", got)
	}
}

func TestUnitSlug(t *testing.T) {
	if got := harness.Slug(
		"A failed scenario's evidence, @postgres!",
	); got != "a-failed-scenario-s-evidence-postgres" {
		t.Errorf("slug = %q", got)
	}
}

// Use Godog's actual selector, including every outline example, rather than
// equating the intended partition with the string used to request it.
func TestUnitPartitionSelections(t *testing.T) {
	all := godogScenarios(t, "")
	owners := map[string][]string{}
	partitions := append(slices.Clone(harness.Labs), harness.Partitions...)
	for _, partition := range partitions {
		t.Run(partition.Name, func(t *testing.T) {
			expr, err := harness.TagExpression(partition.Name)
			if err != nil {
				t.Fatal(err)
			}
			actual := godogScenarios(t, expr)
			for id := range actual {
				owners[id] = append(owners[id], partition.Name)
			}
			for _, selector := range []struct {
				name string
				tags []string
			}{
				{name: "all"},
				{name: "comments", tags: []string{"@case:plan-comments"}},
				{name: "success-or-denial", tags: []string{"@case:success", "@case:denial"}},
			} {
				t.Run(selector.name, func(t *testing.T) {
					selected := godogScenarios(t, harness.And(expr, strings.Join(selector.tags, ",")))
					for id, scenario := range all {
						tags := pickleTags(scenario)
						owner, err := harness.Place(tags)
						if err != nil {
							t.Fatal(err)
						}
						want := owner == partition.Name && (len(selector.tags) == 0 || slices.ContainsFunc(selector.tags, func(tag string) bool {
							return slices.Contains(tags, tag)
						}))
						if _, got := selected[id]; got != want {
							t.Errorf("%s: %s selected=%t, want %t", scenario.Uri, scenario.Name, got, want)
						}
					}
				})
			}
			t.Logf("%d expanded scenarios", len(actual))
		})
	}
	for id, scenario := range all {
		if got := owners[id]; len(got) != 1 {
			t.Errorf("%s: %s has %d owners: %v", scenario.Uri, scenario.Name, len(got), got)
		}
	}
	expr, err := harness.TagExpression("")
	if err != nil {
		t.Fatal(err)
	}
	withoutLabs := godogScenarios(t, expr)
	for id, scenario := range all {
		tags := pickleTags(scenario)
		want := !slices.Contains(tags, "@vcs-lab") && !slices.Contains(tags, "@kubernetes-lab") &&
			!slices.Contains(tags, "@managed-cloud-workers")
		if _, got := withoutLabs[id]; got != want {
			t.Errorf("default selection of %s: %s = %t, want %t", scenario.Uri, scenario.Name, got, want)
		}
	}
}

// The split must preserve the actual parser's complete previous Terraform
// selection, with no duplicated or misplaced scenario in any required job.
func TestUnitTerraformSplitCoverage(t *testing.T) {
	legacy := godogScenarios(t, "~@vcs-lab && ~@kubernetes-lab && ~@managed-cloud-workers && @terraform")
	union := map[string]string{}
	for _, name := range []string{"terraform", "terraform-plans", "terraform-vcs"} {
		expr, err := harness.TagExpression(name)
		if err != nil {
			t.Fatal(err)
		}
		actual := godogScenarios(t, expr)
		if len(actual) == 0 {
			t.Fatalf("required partition %s has no real scenarios", name)
		}
		for id, scenario := range actual {
			if _, ok := legacy[id]; !ok {
				t.Errorf("%s: %s is misplaced in %s", scenario.Uri, scenario.Name, name)
			}
			if previous, ok := union[id]; ok {
				t.Errorf("%s: %s runs in both %s and %s", scenario.Uri, scenario.Name, previous, name)
			}
			union[id] = name
			tags := pickleTags(scenario)
			if name == "terraform-vcs" && !strings.Contains(scenario.Uri, "/vcs/") && !slices.Contains(tags, "@policy-push-range") &&
				!slices.Contains(tags, "@policy-push-workers") {
				t.Errorf("%s: %s is outside the native VCS workflow workload", scenario.Uri, scenario.Name)
			}
			if name == "terraform-plans" && !slices.Contains(tags, "@plan-sanitization") && !slices.Contains(tags, "@policy-plan-worker") {
				t.Errorf("%s: %s is outside the plan artifact workload", scenario.Uri, scenario.Name)
			}
		}
		t.Logf("%s: %d expanded scenarios", name, len(actual))
	}
	for id, scenario := range legacy {
		if _, ok := union[id]; !ok {
			t.Errorf("%s: %s disappeared from Terraform acceptance", scenario.Uri, scenario.Name)
		}
	}
	t.Logf("preserved union: %d expanded scenarios", len(legacy))
}

func godogScenarios(t *testing.T, expr string) map[string]*messages.Pickle {
	t.Helper()
	suite := godog.TestSuite{Options: &godog.Options{Paths: []string{"features"}, Tags: expr}}
	features, err := suite.RetrieveFeatures()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*messages.Pickle{}
	for _, feature := range features {
		for _, scenario := range feature.Pickles {
			// The example AST ID keeps identical outline names distinct.
			out[scenario.Uri+":"+strings.Join(scenario.AstNodeIds, ":")] = scenario
		}
	}
	return out
}

func pickleTags(scenario *messages.Pickle) []string {
	tags := make([]string, 0, len(scenario.Tags))
	for _, tag := range scenario.Tags {
		tags = append(tags, tag.Name)
	}
	return tags
}

// The two jobs must preserve the previous core workload, including outline
// examples, while earlier dependency partitions retain their ownership.
func TestUnitCoreIdentitySplitCoverage(t *testing.T) {
	legacy := godogScenarios(
		t,
		"~@vcs-lab && ~@managed-cloud-workers && ~@terraform-plans && ~@terraform-vcs && ~@terraform && ~@docker && ~@tofu && ~@terragrunt && ~@kubernetes && ~@flows-agent && ~@vcs && ~@github && ~@gitlab && ~@bitbucket-cloud && ~@bitbucket-dc && ~@azure-devops && ~@ai",
	)
	union := map[string]string{}
	for _, name := range []string{"core", "identity"} {
		expr, err := harness.TagExpression(name)
		if err != nil {
			t.Fatal(err)
		}
		selected := godogScenarios(t, expr)
		if len(selected) == 0 {
			t.Fatalf("required partition %s has no scenarios", name)
		}
		for id, scenario := range selected {
			if _, ok := legacy[id]; !ok {
				t.Errorf("%s: %s moved from an earlier dependency partition", scenario.Uri, scenario.Name)
			}
			if prior, ok := union[id]; ok {
				t.Errorf("%s: %s belongs to both %s and %s", scenario.Uri, scenario.Name, prior, name)
			}
			identity := strings.HasPrefix(scenario.Uri, "features/auth/") || strings.HasPrefix(scenario.Uri, "features/authz/")
			if (name == "identity") != identity {
				t.Errorf("%s: %s has incorrect identity ownership %s", scenario.Uri, scenario.Name, name)
			}
			union[id] = name
		}
		t.Logf("%s: %d expanded scenarios", name, len(selected))
	}
	for id, scenario := range legacy {
		if _, ok := union[id]; !ok {
			t.Errorf("%s: %s disappeared from core acceptance", scenario.Uri, scenario.Name)
		}
	}
	t.Logf("preserved core union: %d expanded scenarios", len(legacy))
}

// The two jobs must preserve the previous runs workload, including outline
// examples. run-credentials owns exactly the run scenarios that hand a run an
// OIDC federation token (@oidc) or assumed cloud credentials (features/cloud/),
// so a new one that omits @run-credentials fails here rather than landing in
// runs unseen.
func TestUnitRunCredentialsSplitCoverage(t *testing.T) {
	legacy := godogScenarios(t, harness.And(
		"~@vcs-lab && ~@kubernetes-lab && ~@managed-cloud-workers && ~@terraform-plans && ~@terraform-vcs && ~@terraform",
		"@docker,@tofu,@terragrunt,@kubernetes,@flows-agent",
	))
	union := map[string]string{}
	for _, name := range []string{"run-credentials", "runs"} {
		expr, err := harness.TagExpression(name)
		if err != nil {
			t.Fatal(err)
		}
		selected := godogScenarios(t, expr)
		if len(selected) == 0 {
			t.Fatalf("required partition %s has no scenarios", name)
		}
		for id, scenario := range selected {
			if _, ok := legacy[id]; !ok {
				t.Errorf("%s: %s moved into %s from another partition or carries no run dependency", scenario.Uri, scenario.Name, name)
			}
			if prior, ok := union[id]; ok {
				t.Errorf("%s: %s belongs to both %s and %s", scenario.Uri, scenario.Name, prior, name)
			}
			credentials := slices.Contains(pickleTags(scenario), "@oidc") || strings.HasPrefix(scenario.Uri, "features/cloud/")
			if (name == "run-credentials") != credentials {
				t.Errorf("%s: %s has incorrect run credential ownership %s", scenario.Uri, scenario.Name, name)
			}
			union[id] = name
		}
		t.Logf("%s: %d expanded scenarios", name, len(selected))
	}
	for id, scenario := range legacy {
		if _, ok := union[id]; !ok {
			t.Errorf("%s: %s disappeared from run acceptance", scenario.Uri, scenario.Name)
		}
	}
	t.Logf("preserved runs union: %d expanded scenarios", len(legacy))
}
