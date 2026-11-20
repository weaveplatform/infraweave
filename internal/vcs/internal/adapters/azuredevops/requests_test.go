package azuredevops_test

import (
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitNativeRequestSnapshotAndAvailability(t *testing.T) {
	l := newLab(t)
	l.fake.Repo("Platform", "infra").Branch("target", l.repo.Base)
	if _, err := l.fake.PullRequestWebhook("request", "git.pullrequest.created", "Platform", "infra", azuredevopsfake.WebhookPullRequest{
		ID: 12, SourceBranch: "feature", TargetBranch: "target", Head: l.repo.Head, Fork: true, SourceRepository: "fork", Votes: []int{10},
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := l.az.ReadRequest(t.Context(), l.repo.Repo, 12)
	if err != nil || snapshot.Head != l.repo.Head || snapshot.Base != l.repo.Base || snapshot.Request.Number != 12 || snapshot.Request.AuthorLogin != "author@example.com" ||
		snapshot.Request.HeadBranch != "feature" || snapshot.Request.BaseBranch != "target" || !snapshot.Request.Fork || snapshot.Request.Title != "Infrastructure change" ||
		snapshot.HeadRepository.Name != "fork" || snapshot.BaseRepository.Name != "infra" || snapshot.HeadRepository.ID == snapshot.BaseRepository.ID ||
		snapshot.Approved == nil || !*snapshot.Approved || snapshot.Mergeable != nil || snapshot.Undiverged != nil {
		t.Fatal("native peers, preview commits or optional aggregate facts changed", snapshot, err)
	}
	for _, number := range []int{0, 13} {
		if _, err := l.az.ReadRequest(t.Context(), l.repo.Repo, number); !errors.Is(err, port.ErrNotFound) {
			t.Fatal("missing request accepted", number, err)
		}
	}
	if err := l.fake.SetPullRequest("Platform", "infra", 12, []byte(`{"pullRequestId":99}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.az.ReadRequest(t.Context(), l.repo.Repo, 12); err == nil {
		t.Fatal("different native request accepted")
	}
	l.fake.Fail(azuredevopsfake.ServerError, time.Minute)
	if _, err := l.az.ReadRequest(t.Context(), l.repo.Repo, 12); err == nil {
		t.Fatal("backend failure hidden")
	}
}
