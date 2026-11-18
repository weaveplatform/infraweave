package app

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/test/deps"
)

// A native identity rename changes coordinates, but cannot admit the branch
// head as a tracked commit on behalf of a capped or refused push. This drives
// the canonical event through the real VCS consumer and a real Git server.
func TestAccRepositoryRenamePreservesTrackedCommit(t *testing.T) {
	if err := deps.DockerReachable(t.Context()); err != nil {
		if os.Getenv(sqltest.RequireDocker) != "1" {
			t.Skipf("Gitea: %v (set %s=1 to fail instead)", err, sqltest.RequireDocker)
		}
		t.Fatalf("Gitea: %v", err)
	}
	g, err := runSpecDependencies.Gitea(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	name := "rename-" + strconv.FormatInt(runSpecDependencies.Serial(), 10)
	head, err := g.CreateRepository(t.Context(), name, map[string]string{"main.tf": "terraform {}\n"})
	if err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.VCS.CAFile = g.CAFile
	h := newHoldWorldConfig(t, c)
	box := testEvents(t, h.q)
	h.d, err = newDomains(c, h.db, h.keys, h.blobs.Objects, nil, box, discard())
	if err != nil {
		t.Fatal(err)
	}
	w := eventWorld{holdWorld: h, box: box}
	stack := kernel.NewULID()
	if _, err := w.q.Exec(t.Context(), `INSERT INTO stacks
		(account_id,id,slug,name,space_id,provider,namespace,repository,repository_url,branch,vendor,
		tracked_branch_head,tracked_commit,tracked_commit_set_by)
		VALUES($1,$2,'rename','Rename',$3,'GIT','old','before',$4,'main','terraform',
		'{"hash":"previous-head"}','{"hash":"previous-tracked"}','policy')`,
		w.acct, stack, w.kept, g.CloneURL(name)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Do(t.Context(), w.q, tx.Options{}, func(ctx context.Context) error {
		return w.d.Stacks.Stacks().RepointVCSRepository(ctx, w.acct,
			stacksapi.Use{Provider: "GIT", Default: true},
			stacksapi.VCSRepository{Namespace: "old", Name: "before"},
			stacksapi.VCSRepository{Namespace: "new", Name: "after"})
	}); err != nil {
		t.Fatal(err)
	}
	var event kernel.ULID
	if err := w.q.QueryRow(t.Context(), `SELECT id FROM events
		WHERE account_id=$1 AND kind='stack.changed' AND subject_id=$2`, w.acct, stack.String()).Scan(&event); err != nil {
		t.Fatal(err)
	}
	w.workers(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := deps.Eventually(ctx, "renamed stack's real VCS consumer", func(ctx context.Context) (bool, error) {
		var done bool
		err := w.q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jobs
			WHERE account_id=$1 AND unique_key=$2 AND state='succeeded')`,
			w.acct, event.String()+":vcs.track").Scan(&done)
		return done, err
	}); err != nil {
		t.Fatal(err)
	}
	var branch, tracked, by, namespace, repository string
	var version int64
	if err := w.q.QueryRow(t.Context(), `SELECT tracked_branch_head->>'hash',tracked_commit->>'hash',
		tracked_commit_set_by,namespace,repository,version FROM stacks WHERE account_id=$1 AND id=$2`,
		w.acct, stack).Scan(&branch, &tracked, &by, &namespace, &repository, &version); err != nil {
		t.Fatal(err)
	}
	if branch != "previous-head" || tracked != "previous-tracked" || by != "policy" {
		t.Fatalf("rename bypassed tracked-commit admission: head=%s tracked=%s by=%s native=%s", branch, tracked, by, head)
	}
	if namespace != "new" || repository != "after" || version != 2 {
		t.Fatal("canonical rename lost its coordinates/version", namespace, repository, version)
	}
}
