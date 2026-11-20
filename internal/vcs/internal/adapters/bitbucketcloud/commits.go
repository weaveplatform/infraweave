package bitbucketcloud

import (
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type webhookPushCommit struct {
	Hash    string    `json:"hash"`
	Message string    `json:"message"`
	Date    time.Time `json:"date"`
	Author  struct {
		Raw  string      `json:"raw"`
		User webhookUser `json:"user"`
	} `json:"author"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

func (c webhookChange) pushCommits() *vcsapi.CommitCollection {
	if c.Commits == nil {
		return nil
	}
	omission := ""
	// Bitbucket documents a five-commit cap and an explicit truncated flag.
	if c.Truncated == nil {
		omission = "unknown-commit-count"
	} else if *c.Truncated || len(*c.Commits) > 5 {
		omission = "provider-commit-limit"
	}
	items := make([]vcsapi.PushCommit, 0, len(*c.Commits))
	for _, native := range *c.Commits {
		author := native.Author.User.Nickname
		if author == "" {
			author = native.Author.Raw
		}
		// The documented native commit object supplies no per-commit paths.
		items = append(items, vcsapi.PushCommit{Hash: native.Hash, Author: author, Message: native.Message, URL: native.Links.HTML.Href, At: native.Date})
	}
	return port.NativeCommits("push.changes[].commits", items, omission)
}
