package github

import (
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func (d *webhook) pushCommits() *vcsapi.CommitCollection {
	if d.Commits == nil {
		return nil
	}
	omission := ""
	// GitHub documents at most 2048 webhook commits. At the boundary there
	// is no reliable total count, so the collection is conservatively unknown.
	if len(*d.Commits) >= 2048 {
		omission = "provider-commit-limit"
	}
	items := make([]vcsapi.PushCommit, 0, len(*d.Commits))
	for _, native := range *d.Commits {
		item := vcsapi.PushCommit{Hash: native.GetID(), Author: native.GetAuthor().GetName(), Message: native.GetMessage(), URL: native.GetURL()}
		if login := native.GetAuthor().GetLogin(); login != "" {
			item.Author = login
		}
		if native != nil {
			if native.Timestamp != nil {
				item.At = native.Timestamp.Time
			}
			if native.Added != nil && native.Removed != nil && native.Modified != nil {
				files := append(append(append([]string{}, native.Added...), native.Removed...), native.Modified...)
				item.Files = &files
			}
		}
		items = append(items, item)
	}
	return port.NativeCommits("commits", items, omission)
}
