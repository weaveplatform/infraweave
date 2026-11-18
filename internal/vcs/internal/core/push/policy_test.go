package push_test

import (
	"strings"
	"testing"

	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

func TestUnitDefaultPushBehavior(t *testing.T) {
	for _, tc := range []struct {
		name, kind, branch, root string
		files, globs             []string
		want                     push.Decision
	}{
		{"tracked root change", "push", "main", "terraform", []string{"terraform/main.tf"}, nil, push.Decision{Outcome: push.Track}},
		{"tracked additional recursive glob", "push", "main", "terraform", []string{"shared/deep/vars.tf"}, []string{"shared/**/*.tf"}, push.Decision{Outcome: push.Track}},
		{"proposed without request", "push", "feature", "terraform", []string{"terraform/main.tf"}, nil, push.Decision{Outcome: push.Propose}},
		{"tracked unrelated change", "push", "main", "terraform", []string{"README.md"}, nil, push.Decision{push.Ignored, push.NoAffectedFiles}},
		{"root boundary", "push", "main", "terraform", []string{"terraform-other/main.tf"}, nil, push.Decision{push.Ignored, push.NoAffectedFiles}},
		{"proposed unrelated change", "push", "feature", "terraform", []string{"README.md"}, nil, push.Decision{Outcome: push.NoAction}},
		{"tag stands alone", "tag", "", "terraform", []string{"README.md"}, nil, push.Decision{push.Ignored, push.TagsIgnored}},
		{"tag with affected root", "tag", "", "terraform", []string{"terraform/main.tf"}, nil, push.Decision{push.Ignored, push.TagsIgnored}},
		{"pull request", "pull_request", "feature", "terraform", []string{"terraform/main.tf"}, nil, push.Decision{Outcome: push.NoAction}},
		{"comment", "comment", "main", "terraform", []string{"terraform/main.tf"}, nil, push.Decision{Outcome: push.NoAction}},
		{"empty root", "push", "main", "", []string{"deep/main.tf"}, nil, push.Decision{Outcome: push.Track}},
		{"normalized root", "push", "main", "./terraform/", []string{"/terraform/main.tf"}, nil, push.Decision{Outcome: push.Track}},
		{"no diff", "push", "main", "", nil, nil, push.Decision{push.Ignored, push.NoAffectedFiles}},
		{"invalid glob", "push", "main", "terraform", []string{"README.md"}, []string{"["}, push.Decision{push.Ignored, push.NoAffectedFiles}},
		{"escaping file", "push", "main", "", []string{"../escape.tf"}, nil, push.Decision{push.Ignored, push.NoAffectedFiles}},
		{"root marker is not file", "push", "main", ".", []string{"."}, nil, push.Decision{push.Ignored, push.NoAffectedFiles}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stack := stacksapi.Settings{Branch: "main", ProjectRoot: &tc.root, AdditionalProjectGlobs: tc.globs}
			got := push.Default(vcsapi.Event{Type: tc.kind, Branch: tc.branch}, stack, tc.files)
			if got != tc.want {
				t.Fatalf("decision=%+v want=%+v", got, tc.want)
			}
		})
	}
	if got := push.Default(vcsapi.Event{Type: "push", Branch: "main"}, stacksapi.Settings{Branch: "main"}, []string{"main.tf"}); got.Outcome != push.Track {
		t.Fatal("nil project root did not mean the whole repository")
	}
	if got := push.Default(
		vcsapi.Event{Type: "push", Branch: "main", Head: strings.Repeat("0", 40)},
		stacksapi.Settings{Branch: "main"},
		[]string{"main.tf"},
	); got.Outcome != push.NoAction {
		t.Fatal("deleted branch did work")
	}
}
