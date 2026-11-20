package gitlab_test

import (
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitNativeRequestSnapshotAndAvailability(t *testing.T) {
	l := newLab(t)
	l.fake.AddMember("contributor", "fork", "infraweave", gitlabfake.Developer)
	if _, err := l.fake.MergeRequestWebhook("acme/platform", "infra", gitlabfake.WebhookMergeRequest{
		IID: 12, SourceBranch: "feature", TargetBranch: "main", Head: l.repo.Head, Author: "creator", Actor: "reviewer",
		SourceNamespace: "contributor", SourceRepository: "fork", Labels: []string{"infra"}, DetailedMergeStatus: "mergeable",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := l.gl.ReadRequest(t.Context(), l.repo.Repo, 12)
	if err != nil || snapshot.Head != l.repo.Head || snapshot.Base != l.repo.Head || snapshot.Request.Number != 12 || snapshot.Request.AuthorLogin != "creator" ||
		snapshot.Request.HeadBranch != "feature" || snapshot.Request.BaseBranch != "main" || !snapshot.Request.Fork || snapshot.Request.Title != "Infrastructure change" ||
		snapshot.HeadRepository.Name != "fork" || snapshot.BaseRepository.Name != "infra" || snapshot.HeadRepository.ID == snapshot.BaseRepository.ID ||
		snapshot.Approved == nil || *snapshot.Approved || snapshot.Mergeable == nil || !*snapshot.Mergeable || snapshot.Undiverged == nil || !*snapshot.Undiverged {
		t.Fatal("native peers, diff refs or approval state changed", snapshot, err)
	}
	for _, number := range []int{0, 13} {
		if _, err := l.gl.ReadRequest(t.Context(), l.repo.Repo, number); !errors.Is(err, port.ErrNotFound) {
			t.Fatal("missing request accepted", number, err)
		}
	}
	l.fake.Fail(gitlabfake.ServerError, time.Minute)
	if _, err := l.gl.ReadRequest(t.Context(), l.repo.Repo, 12); err == nil {
		t.Fatal("backend failure hidden")
	}
}
