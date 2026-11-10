package sourcetest

import (
	"context"
	"errors"
	"testing"
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var errOffline = errors.New("offline")

// source answers every call with commit, file and paths, or err, and
// has no source URL.
type source struct {
	commit vcsapi.Commit
	file   []byte
	paths  []string
	err    error
}

func (s source) ResolveRef(context.Context, port.Repository, string) (vcsapi.Commit, error) {
	return s.commit, s.err
}

func (s source) ReadFile(context.Context, port.Repository, string, string) ([]byte, error) {
	return s.file, s.err
}

func (s source) AffectedFiles(context.Context, port.Repository, string, string) ([]string, error) {
	return s.paths, s.err
}

func (source) SourceURL(port.Repository, string) string { return "" }

var repo = Repository{
	Repo:    port.Repository{Name: "infra"},
	Missing: port.Repository{Name: "gone"},
	Branch:  "main",
	Tag:     "v1",
	Tagged:  "aaa",
	Base:    "bbb",
	Head:    "ccc",
	File:    "main.tf",
	Content: []byte("# main\n"),
	Changed: []string{"main.tf"},
}

// TestUnitCheck checks that a Source failing every call, and one
// answering every call alike, break every clause they fail, each as
// ErrContract.
func TestUnitCheck(t *testing.T) {
	for name, c := range map[string]struct {
		src     port.Source
		clauses int
	}{
		// Every clause.
		"failing": {source{err: errOffline}, 12},
		// The tag's and the base's resolves, the head's message, every
		// refusal and the source URL.
		"alike": {source{
			commit: vcsapi.Commit{Hash: "ccc"},
			file:   []byte("# main\n"),
			paths:  []string{"main.tf"},
		}, 9},
	} {
		t.Run(name, func(t *testing.T) {
			err := Check(t.Context(), c.src, repo)
			if !errors.Is(err, ErrContract) {
				t.Fatalf("Check = %v", err)
			}
			if n := len(err.(interface{ Unwrap() []error }).Unwrap()); n != c.clauses {
				t.Errorf("Check broke %d clauses, want %d: %v", n, c.clauses, err)
			}
		})
	}
}

// TestUnitRun checks that Run passes a Source keeping the contract.
func TestUnitRun(t *testing.T) {
	Run(t, keeper{}, repo)
}

// keeper keeps the contract on repo.
type keeper struct{}

func (keeper) ResolveRef(_ context.Context, r port.Repository, ref string) (vcsapi.Commit, error) {
	c := vcsapi.Commit{Message: "m", AuthorName: "a", Timestamp: time.Unix(1, 0)}
	switch {
	case r != repo.Repo:
		return vcsapi.Commit{}, port.ErrNotFound
	case ref == repo.Branch:
		c.Hash = repo.Head
	case ref == repo.Tag:
		c.Hash, c.Tag = repo.Tagged, repo.Tag
	case ref == repo.Base:
		c.Hash = repo.Base
	default:
		return vcsapi.Commit{}, port.ErrNotFound
	}
	return c, nil
}

func (keeper) ReadFile(_ context.Context, _ port.Repository, _, path string) ([]byte, error) {
	if path != repo.File {
		return nil, port.ErrNotFound
	}
	return repo.Content, nil
}

func (keeper) AffectedFiles(
	_ context.Context,
	_ port.Repository,
	base, _ string,
) ([]string, error) {
	if base != repo.Base {
		return nil, port.ErrNotFound
	}
	return repo.Changed, nil
}

func (keeper) SourceURL(port.Repository, string) string { return "https://git.example/r.git" }
