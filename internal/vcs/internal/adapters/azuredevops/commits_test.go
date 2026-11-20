package azuredevops

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnitNativePushCommitsRetainUnavailablePathFacts(t *testing.T) {
	var r webhookResource
	if err := json.Unmarshal(
		[]byte(
			`{"refUpdates":[{"name":"refs/heads/main"}],"commits":[{"commitId":"`+strings.Repeat(
				"a",
				40,
			)+`","comment":"native message","url":"https://example.com/c","author":{"name":"Author","date":"2026-11-20T10:00:00Z"}}]}`,
		),
		&r,
	); err != nil {
		t.Fatal(err)
	}
	got := r.pushCommits()
	if got.Source != "resource.commits" || got.Omission != "missing-commit-files" || len(got.Items) != 1 || got.Items[0].Author != "Author" ||
		got.Items[0].Message != "native message" ||
		got.Items[0].Files != nil {
		t.Fatal(got)
	}
	r.RefUpdates = append(r.RefUpdates, webhookRefUpdate{Name: "refs/heads/other"})
	if got := r.pushCommits(); got.Omission != "multiple-ref-updates" {
		t.Fatal(got)
	}
	r.Commits = nil
	if r.pushCommits() != nil {
		t.Fatal("absent native commits became known empty")
	}
}
