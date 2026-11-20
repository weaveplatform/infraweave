package github

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	githubsdk "github.com/google/go-github/v93/github"
)

func TestUnitNativePushCommitsPreserveCompleteMetadata(t *testing.T) {
	var d webhook
	if err := json.Unmarshal(
		[]byte(
			`{"commits":[{"id":"`+strings.Repeat(
				"a",
				40,
			)+`","message":"native message","timestamp":"2026-11-20T10:00:00Z","url":"https://example.com/commit","author":{"name":"Author","username":"login"},"added":["a.tf"],"modified":["b.tf"],"removed":[]}]}`,
		),
		&d,
	); err != nil {
		t.Fatal(err)
	}
	c := d.pushCommits()
	if c.Omission != "" || len(c.Items) != 1 || c.Items[0].Author != "login" || c.Items[0].Message != "native message" ||
		!slices.Equal(*c.Items[0].Files, []string{"a.tf", "b.tf"}) {
		t.Fatal(c)
	}
	for _, mode := range []string{"missing", "null item", "missing paths", "cap", "above cap", "name only", "empty"} {
		t.Run(mode, func(t *testing.T) {
			copy := d
			items := slices.Clone(*d.Commits)
			copy.Commits = &items
			switch mode {
			case "missing":
				copy.Commits = nil
			case "null item":
				items[0] = nil
			case "missing paths":
				v := *items[0]
				v.Modified = nil
				items[0] = &v
			case "cap":
				items = make([]*githubsdk.HeadCommit, 2048)
			case "above cap":
				items = make([]*githubsdk.HeadCommit, 2049)
			case "name only":
				v := *items[0]
				a := *v.Author
				a.Login = nil
				v.Author = &a
				items[0] = &v
			case "empty":
				items = []*githubsdk.HeadCommit{}
			}
			got := copy.pushCommits()
			if mode == "missing" {
				if got != nil {
					t.Fatal(got)
				}
				return
			}
			if mode == "cap" || mode == "above cap" {
				if got.Omission != "provider-commit-limit" {
					t.Fatal(got)
				}
				return
			}
			if mode == "null item" || mode == "missing paths" {
				if got.Omission == "" {
					t.Fatal("incomplete native commits exposed", got)
				}
				return
			}
			if got.Omission != "" || mode == "name only" && got.Items[0].Author != "Author" {
				t.Fatal(got)
			}
		})
	}
}
