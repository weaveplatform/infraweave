package gitlab

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestUnitNativePushCommitsProveTheirTotal(t *testing.T) {
	var d webhookPayload
	if err := json.Unmarshal(
		[]byte(
			`{"total_commits_count":1,"commits":[{"id":"`+strings.Repeat(
				"a",
				40,
			)+`","message":"native message","timestamp":"2026-11-20 10:00:00 UTC","url":"https://example.com/commit","author":{"name":"Author"},"added":["a.tf"],"modified":[],"removed":[]}]}`,
		),
		&d,
	); err != nil {
		t.Fatal(err)
	}
	got := d.pushCommits()
	if got.Omission != "" || len(got.Items) != 1 || got.Items[0].Author != "Author" || !slices.Equal(*got.Items[0].Files, []string{"a.tf"}) {
		t.Fatal(got)
	}
	for _, mode := range []string{"missing", "unknown total", "truncated", "over cap", "missing paths", "empty"} {
		t.Run(mode, func(t *testing.T) {
			copy := d
			items := slices.Clone(*d.Commits)
			copy.Commits = &items
			switch mode {
			case "missing":
				copy.Commits = nil
			case "unknown total":
				copy.TotalCommits = nil
			case "truncated":
				copy.TotalCommits = new(int64(21))
			case "over cap":
				items = make([]webhookPushCommit, 21)
				copy.TotalCommits = new(int64(21))
			case "missing paths":
				items[0].Removed = nil
			case "empty":
				items = []webhookPushCommit{}
				copy.TotalCommits = new(int64(0))
			}
			got := copy.pushCommits()
			if mode == "missing" {
				if got != nil {
					t.Fatal(got)
				}
				return
			}
			if mode == "empty" {
				if got.Omission != "" || len(got.Items) != 0 {
					t.Fatal(got)
				}
				return
			}
			if got.Omission == "" {
				t.Fatal("unproven native commit collection exposed", got)
			}
		})
	}
}
