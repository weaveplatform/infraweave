package rawgit

import (
	"context"
	"crypto/x509"
	"os"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
	"github.com/weaveplatform/infraweave/test/deps"
)

// TestAccSourceGitea checks that Source keeps the contract over a Gitea
// repository, which git itself serves: refs it advertises, commits fetched
// one deep, and a want no ref reaches refused. Without a Docker daemon it
// skips, unless sqltest.RequireDocker is 1, as CI sets it.
func TestAccSourceGitea(t *testing.T) {
	if err := deps.DockerReachable(t.Context()); err != nil {
		if os.Getenv(sqltest.RequireDocker) != "1" {
			t.Skipf("Gitea: %v (set %s=1 to fail instead)", err, sqltest.RequireDocker)
		}
		t.Fatalf("Gitea: %v", err)
	}
	var d deps.Dependencies
	// t.Context has ended by the time cleanups run.
	t.Cleanup(func() { _ = d.Close(context.Background()) })
	g, err := d.Gitea(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	first, err := g.CreateRepository(ctx, "infra", map[string]string{
		"README.md": "# infra\n",
		"main.tf":   "# one\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Tag(ctx, "infra", "v1", first); err != nil {
		t.Fatal(err)
	}
	base, err := g.Commit(ctx, "infra", "main", map[string]string{"main.tf": "# two\n"},
		"Change the stack")
	if err != nil {
		t.Fatal(err)
	}
	head, err := g.Commit(ctx, "infra", "main", map[string]string{
		".infraweave/config.yml": "version: 1\n",
		"main.tf":                "# three\n",
	}, "Configure the stack")
	if err != nil {
		t.Fatal(err)
	}
	ca, err := os.ReadFile(g.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("Gitea's CA file holds no certificate")
	}
	sourcetest.Run(t, New(Options{RootCAs: roots}), sourcetest.Repository{
		Repo:    port.Repository{Name: "infra", URL: g.CloneURL("infra")},
		Missing: port.Repository{Name: "gone", URL: g.CloneURL("gone")},
		Branch:  "main",
		Tag:     "v1",
		Tagged:  first,
		Base:    base,
		Head:    head,
		File:    ".infraweave/config.yml",
		Content: []byte("version: 1\n"),
		Changed: []string{".infraweave/config.yml", "main.tf"},
	})
}
