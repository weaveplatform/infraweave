package acceptance

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Partition is one CI job of decision 0016's acceptance split, named by the
// suffix make accept PARTITION= takes.
type Partition struct {
	Name string
	// Tags are the dependency tags whose scenarios run in the partition.
	Tags []string
}

// Partitions is decision 0016's table, in the order a scenario is placed:
// the first partition that lists one of its tags takes it, so a scenario
// that needs a run and a VCS runs with the runs. accept-core also takes
// every untagged scenario.
var Partitions = []Partition{
	{Name: "runs", Tags: []string{"@terraform", "@tofu", "@kubernetes", "@flows-agent"}},
	{Name: "integrations", Tags: []string{"@vcs", "@ai"}},
	{Name: "core", Tags: []string{"@postgres", "@objectstore", "@oidc"}},
}

// Tags that place no scenario: @op:, @case: and @provider: name what a
// scenario evidences (specref parity --states), and @quarantine marks one
// that is known to be unreliable.
var exemptPrefixes = []string{"@op:", "@case:", "@provider:"}

const quarantine = "@quarantine"

// Exempt reports whether tag places no scenario in a partition.
func Exempt(tag string) bool {
	return tag == quarantine || slices.ContainsFunc(exemptPrefixes, func(p string) bool {
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
		if !Exempt(t) && !slices.ContainsFunc(Partitions, func(p Partition) bool {
			return slices.Contains(p.Tags, t)
		}) {
			return "", fmt.Errorf("%w: %s", ErrUnlistedTag, t)
		}
	}
	for _, p := range Partitions {
		if slices.ContainsFunc(tags, func(t string) bool { return slices.Contains(p.Tags, t) }) {
			return p.Name, nil
		}
	}
	return "core", nil
}

// TagExpression is the godog tag expression that selects the scenarios Place
// puts in the partition called name; empty selects every scenario.
func TagExpression(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	var not []string
	for _, p := range Partitions {
		if p.Name == name {
			// A scenario of an earlier partition belongs there, so it is
			// excluded here; godog joins "," as or and "&&" as and.
			terms := slices.Clone(not)
			if name != "core" {
				terms = append(terms, strings.Join(p.Tags, ","))
			}
			return strings.Join(terms, " && "), nil
		}
		for _, t := range p.Tags {
			not = append(not, "~"+t)
		}
	}
	return "", fmt.Errorf("%w %q", ErrUnknownPartition, name)
}
