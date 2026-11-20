package bitbucketcloud_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Native request responses follow the provider's released GET request contract;
// webhook actors and delivery state do not supply this independent snapshot.
func TestUnitNativeRequestSnapshotAndAvailability(t *testing.T) {
	l := newLab(t)

	target := l.fake.RepositoryPayload("acme", "infra")
	source := l.fake.RepositoryPayload("contributor", "fork")

	body, err := bitbucketcloudfake.Payload("pullrequest:created")
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	r := envelope["pullrequest"].(map[string]any)
	r["source"] = map[string]any{"branch": map[string]string{"name": "feature"}, "commit": map[string]string{"hash": l.repo.Head}, "repository": source}
	r["destination"] = map[string]any{"branch": map[string]string{"name": "main"}, "commit": map[string]string{"hash": l.repo.Base}, "repository": target}

	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.fake.SetPullRequest("acme", "infra", 12, encoded); err != nil {
		t.Fatal(err)
	}
	snapshot, err := l.bb.ReadRequest(t.Context(), l.repo.Repo, 12)
	if err != nil || snapshot.Head != l.repo.Head || snapshot.Base != l.repo.Base || snapshot.Request.Number != 12 || snapshot.Request.AuthorLogin != "author" ||
		snapshot.Request.HeadBranch != "feature" || snapshot.Request.BaseBranch != "main" || !snapshot.Request.Fork || snapshot.Request.Title != "Infrastructure change" ||
		snapshot.HeadRepository.Name != "fork" || snapshot.BaseRepository.Name != "infra" || snapshot.HeadRepository.ID == snapshot.BaseRepository.ID {
		t.Fatal("native peers or immutable request metadata changed", snapshot, err)
	}
	for _, number := range []int{0, 13} {
		if _, err := l.bb.ReadRequest(t.Context(), l.repo.Repo, number); !errors.Is(err, port.ErrNotFound) {
			t.Fatal("missing request accepted", number, err)
		}
	}
	r["id"] = 99
	encoded, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.fake.SetPullRequest("acme", "infra", 12, encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := l.bb.ReadRequest(t.Context(), l.repo.Repo, 12); err == nil {
		t.Fatal("different native request accepted")
	}
	l.fake.Fail(bitbucketcloudfake.ServerError, time.Minute)
	if _, err := l.bb.ReadRequest(t.Context(), l.repo.Repo, 12); err == nil {
		t.Fatal("backend failure hidden")
	}
}
