package harness

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Partition is one job of the acceptance split, a pull request's CI job
// or a scheduled lab's, named by the suffix make accept PARTITION= takes.
//
// ref: decision 0016
type Partition struct {
	Name string
	// Tags are the dependency tags whose scenarios run in the partition.
	Tags []string
}

// Labs are the scheduled jobs' partitions, whose scenarios reach
// infrastructure that only the job provisions from test/terraform: the
// real VCS providers' repositories of test/terraform/vcs, for vcs-lab.
// A lab's scenario is placed there before any partition of Partitions
// sees it, and every partition of Partitions, and a run of none, excludes
// it.
//
// ref: decision 0016
var Labs = []Partition{
	{Name: "vcs-lab", Tags: []string{"@vcs-lab"}},
}

// Partitions are the pull request's CI jobs, in the order a scenario is
// placed: the first partition that lists one of its tags takes it, so a
// scenario with @terraform goes to terraform even when it needs a VCS;
// other run scenarios go to runs. accept-core also takes every untagged scenario.
//
// ref: decision 0016
var Partitions = []Partition{
	{
		Name: "terraform",
		Tags: []string{"@terraform"},
	},
	{
		Name: "runs",
		Tags: []string{"@docker", "@tofu", "@terragrunt", "@kubernetes", "@flows-agent"},
	},
	{
		Name: "integrations",
		Tags: []string{
			"@vcs",
			"@github",
			"@gitlab",
			"@bitbucket-cloud",
			"@bitbucket-dc",
			"@azure-devops",
			"@ai",
		},
	},
	{Name: "core", Tags: []string{"@postgres", "@objectstore", "@oidc", "@vault"}},
}

// Tags that place no scenario: @op:, @case: and @provider: name what a
// scenario evidences (specref parity --states), @lab: names the provider
// whose lab a @vcs-lab scenario reaches, @quarantine marks one that is known
// to be unreliable, and @runtime-config-reject sets the scenario's server
// policy without introducing a dependency. @webhook-controls selects the
// webhook-control cases and @plan-sanitization selects worker plan cases,
// without changing their dependency partitions. @policy-management likewise
// selects policy management cases without changing their dependencies.
var exemptPrefixes = []string{"@op:", "@case:", "@provider:", "@lab:"}

var exemptTags = []string{"@quarantine", "@runtime-config-reject", "@webhook-controls", "@plan-sanitization", "@policy-management", "@policy-attachments"}

// placing is every partition in the order Place tries them: the labs,
// then the pull request's.
func placing() []Partition { return append(slices.Clone(Labs), Partitions...) }

// Exempt reports whether tag places no scenario in a partition.
func Exempt(tag string) bool {
	return slices.Contains(exemptTags, tag) || slices.ContainsFunc(exemptPrefixes, func(p string) bool {
		return strings.HasPrefix(tag, p)
	})
}

// ErrUnknownPartition is returned for a partition name the table lacks.
var ErrUnknownPartition = errors.New("unknown acceptance partition")

// ErrUnlistedTag is returned for a scenario tag no partition lists.
var ErrUnlistedTag = errors.New("no partition lists the tag")

// Place returns the partition a scenario with tags runs in, or
// ErrUnlistedTag naming the first tag no partition lists.
func Place(tags []string) (string, error) {
	for _, t := range tags {
		if !Exempt(t) && !slices.ContainsFunc(placing(), func(p Partition) bool {
			return slices.Contains(p.Tags, t)
		}) {
			return "", fmt.Errorf("%w: %s", ErrUnlistedTag, t)
		}
	}
	for _, p := range placing() {
		if slices.ContainsFunc(tags, func(t string) bool { return slices.Contains(p.Tags, t) }) {
			return p.Name, nil
		}
	}
	return "core", nil
}

// TagExpression is the godog tag expression that selects the scenarios Place
// puts in the partition called name; empty selects every scenario but the
// labs'.
func TagExpression(name string) (string, error) {
	var not []string
	for _, p := range placing() {
		if p.Name == name {
			// A scenario of an earlier partition belongs there, so it is
			// excluded here; godog joins "," as or and "&&" as and.
			terms := slices.Clone(not)
			if name != "core" {
				terms = append(terms, strings.Join(p.Tags, ","))
			}
			return And(terms...), nil
		}
		for _, t := range p.Tags {
			not = append(not, "~"+t)
		}
	}
	if name == "" {
		var labs []string
		for _, p := range Labs {
			for _, t := range p.Tags {
				labs = append(labs, "~"+t)
			}
		}
		return strings.Join(labs, " && "), nil
	}
	return "", fmt.Errorf("%w %q", ErrUnknownPartition, name)
}

// And joins the non-empty godog tag expressions exprs, each of which a
// scenario must match.
func And(exprs ...string) string {
	var clauses []string
	for _, expr := range exprs {
		if expr == "" {
			continue
		}
		terms := strings.Split(expr, ",")
		if len(clauses) == 0 {
			clauses = terms
			continue
		}
		var combined []string
		for _, clause := range clauses {
			for _, term := range terms {
				combined = append(combined, clause+" && "+term)
			}
		}
		clauses = combined
	}
	// Godog splits comma alternatives before evaluating their && terms;
	// distribute conjunctions so exclusions and focus tags apply to every arm.
	return strings.Join(clauses, ",")
}
