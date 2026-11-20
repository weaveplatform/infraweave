package bitbucketdc_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Native request responses follow the provider's released GET request contract;
// webhook actors and delivery state do not supply this independent snapshot.
func TestUnitNativeRequestSnapshotAndAvailability(t *testing.T) {
	l := newLab(t)

	target := l.fake.RepositoryPayload("ACME", "infra")
	source := l.fake.RepositoryPayload("CONTRIBUTOR", "fork")

	body, err := bitbucketdcfake.Payload("pr:opened")
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	r := envelope["pullRequest"].(map[string]any)
	r["fromRef"] = map[string]any{"id": "refs/heads/feature", "displayId": "feature", "latestCommit": l.repo.Head, "repository": source}
	r["toRef"] = map[string]any{"id": "refs/heads/main", "displayId": "main", "latestCommit": l.repo.Base, "repository": target}

	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.fake.SetPullRequest("ACME", "infra", 12, encoded); err != nil {
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
	if err := l.fake.SetPullRequest("ACME", "infra", 12, encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := l.bb.ReadRequest(t.Context(), l.repo.Repo, 12); err == nil {
		t.Fatal("different native request accepted")
	}
	l.fake.Fail(bitbucketdcfake.ServerError, time.Minute)
	if _, err := l.bb.ReadRequest(t.Context(), l.repo.Repo, 12); err == nil {
		t.Fatal("backend failure hidden")
	}
}
