package bitbucketcloud

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnitNativePushCommitsPreserveMetadataWithoutInventingFiles(t *testing.T) {
	var c webhookChange
	if err := json.Unmarshal(
		[]byte(
			`{"truncated":false,"commits":[{"hash":"`+strings.Repeat(
				"a",
				40,
			)+`","message":"native message","date":"2026-11-20T10:00:00Z","author":{"raw":"Author <a@example.com>","user":{"nickname":"login"}},"links":{"html":{"href":"https://example.com/c"}}}]}`,
		),
		&c,
	); err != nil {
		t.Fatal(err)
	}
	got := c.pushCommits()
	if got.Source != "push.changes[].commits" || got.Omission != "missing-commit-files" || len(got.Items) != 1 || got.Items[0].Author != "login" ||
		got.Items[0].URL != "https://example.com/c" ||
		got.Items[0].Files != nil {
		t.Fatal(got)
	}
	for _, mode := range []string{"missing", "unknown", "truncated", "over cap", "raw author", "empty"} {
		t.Run(mode, func(t *testing.T) {
			copy := c
			items := append([]webhookPushCommit{}, (*c.Commits)...)
			copy.Commits = &items
			want := "missing-commit-files"
			switch mode {
			case "missing":
				copy.Commits = nil
			case "unknown":
				copy.Truncated = nil
				want = "unknown-commit-count"
			case "truncated":
				copy.Truncated = new(true)
				want = "provider-commit-limit"
			case "over cap":
				items = make([]webhookPushCommit, 6)
				want = "provider-commit-limit"
			case "raw author":
				items[0].Author.User.Nickname = ""
			case "empty":
				items = []webhookPushCommit{}
				want = ""
			}
			got := copy.pushCommits()
			if mode == "missing" {
				if got != nil {
					t.Fatal(got)
				}
				return
			}
			if got.Omission != want || mode == "raw author" && got.Items[0].Author != "Author <a@example.com>" {
				t.Fatal(got)
			}
		})
	}
}
