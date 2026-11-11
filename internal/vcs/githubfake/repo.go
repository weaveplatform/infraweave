package githubfake

import (
	"fmt"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/gitmem"
)

// Author is the name, e-mail and login every seeded commit carries.
const (
	AuthorName  = gitmem.AuthorName
	AuthorEmail = gitmem.AuthorEmail
	AuthorLogin = "author"
)

// Repo is a repository the fake serves, held in memory by go-git, so refs,
// annotated tags, trees and their differences behave as Git's do.
type Repo = gitmem.Repo

// must panics on a seeding error: the fake's seeding has no failure a test
// recovers from.
func must(err error) {
	if err != nil {
		panic(fmt.Sprintf("githubfake: %v", err))
	}
}
