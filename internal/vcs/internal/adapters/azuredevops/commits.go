package azuredevops

import (
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type webhookPushCommit struct {
	CommitID string `json:"commitId"`
	Comment  string `json:"comment"`
	URL      string `json:"url"`
	Author   struct {
		Name string    `json:"name"`
		Date time.Time `json:"date"`
	} `json:"author"`
}

func (r webhookResource) pushCommits() *vcsapi.CommitCollection {
	if r.Commits == nil {
		return nil
	}
	omission := ""
	// A multi-ref push does not attribute its shared commit array to a ref.
	if len(r.RefUpdates) != 1 {
		omission = "multiple-ref-updates"
	}
	items := make([]vcsapi.PushCommit, 0, len(*r.Commits))
	for _, native := range *r.Commits {
		// Azure's service-hook example has metadata without per-commit paths.
		items = append(items, vcsapi.PushCommit{Hash: native.CommitID, Author: native.Author.Name, Message: native.Comment, URL: native.URL, At: native.Author.Date})
	}
	return port.NativeCommits("resource.commits", items, omission)
}
