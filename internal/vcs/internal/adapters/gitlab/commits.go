package gitlab

import (
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type webhookPushCommit struct {
	ID        string      `json:"id"`
	Message   string      `json:"message"`
	URL       string      `json:"url"`
	Timestamp webhookTime `json:"timestamp"`
	Author    struct {
		Name string `json:"name"`
	} `json:"author"`
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
	Removed  []string `json:"removed"`
}

func (d webhookPayload) pushCommits() *vcsapi.CommitCollection {
	if d.Commits == nil {
		return nil
	}
	omission := ""
	// GitLab sends only the newest twenty commits when the total is larger.
	if d.TotalCommits == nil {
		omission = "unknown-commit-count"
	} else if *d.TotalCommits != int64(len(*d.Commits)) || len(*d.Commits) > 20 {
		omission = "provider-commit-limit"
	}
	items := make([]vcsapi.PushCommit, 0, len(*d.Commits))
	for _, native := range *d.Commits {
		item := vcsapi.PushCommit{Hash: native.ID, Author: native.Author.Name, Message: native.Message, URL: native.URL, At: native.Timestamp.Time}
		if native.Added != nil && native.Modified != nil && native.Removed != nil {
			files := append(append(append([]string{}, native.Added...), native.Modified...), native.Removed...)
			item.Files = &files
		}
		items = append(items, item)
	}
	return port.NativeCommits("commits", items, omission)
}
