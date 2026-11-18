package http

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

type requestViewStore struct{ core.Store }

func (requestViewStore) Starred(context.Context, kernel.AccountID, []kernel.ULID, core.Actor) (map[kernel.ULID]bool, error) {
	return map[kernel.ULID]bool{}, nil
}

type requestViewSpaces struct{ accessapi.Spaces }

func (requestViewSpaces) Slugs(context.Context, kernel.AccountID) (map[kernel.ULID]string, error) {
	return map[kernel.ULID]string{}, nil
}

type requestViewAuth struct{ accessapi.Authorizer }

func (requestViewAuth) Actions(context.Context, kernel.Principal, kernel.ULID) ([]accessapi.Action, error) {
	return []accessapi.Action{accessapi.ActionSpaceRead}, nil
}

type requestReader struct {
	account  kernel.AccountID
	settings []stacksapi.Settings
	rows     map[kernel.ULID][]stacksapi.PullRequest
	err      error
	calls    int
}

func (r *requestReader) PullRequests(_ context.Context, account kernel.AccountID, settings []stacksapi.Settings) (map[kernel.ULID][]stacksapi.PullRequest, error) {
	r.calls++
	r.account = account
	r.settings = settings
	return r.rows, r.err
}

func TestUnitRequestViewBatchAndLateBinding(t *testing.T) {
	t.Parallel()
	service := core.New(core.Deps{Store: requestViewStore{}})
	server := New(service, requestViewAuth{}, requestViewSpaces{}, nil)
	copy := server
	account := kernel.NewAccountID()
	principal := kernel.Principal{AccountID: account}
	settings := []stacksapi.Settings{
		{
			Stack:        stacksapi.Stack{ID: kernel.NewULID(), Slug: "one", Space: account.ULID},
			Provider:     "GITLAB",
			Namespace:    "acme",
			Repository:   "infra",
			Branch:       "main",
			VendorConfig: []byte(`{}`),
		},
		{
			Stack:        stacksapi.Stack{ID: kernel.NewULID(), Slug: "two", Space: account.ULID},
			Provider:     "GITLAB",
			Namespace:    "acme",
			Repository:   "other",
			Branch:       "release",
			VendorConfig: []byte(`{}`),
		},
	}
	stacks := []core.Stack{{Settings: settings[0]}, {Settings: settings[1]}}
	views, err := copy.views(t.Context(), principal, stacks...)
	if err != nil || len(views) != 2 || views[0].PullRequests == nil || len(views[0].PullRequests) != 0 {
		t.Fatal("unbound domain is not empty", views, err)
	}
	created := time.Unix(1_760_000_000, 0).UTC()
	run := kernel.NewULID()
	request := stacksapi.PullRequest{
		ID:            "17",
		AuthorLogin:   "creator",
		Title:         "changes",
		URL:           "https://example.com/17",
		HeadBranch:    "feature",
		HeadBranchURL: "https://example.com/feature",
		HeadSHA:       "delivered-head",
		Draft:         true,
		CreatedAt:     created,
		UpdatedAt:     created.Add(time.Minute),
		Runs:          []kernel.ULID{run},
	}
	reader := &requestReader{rows: map[kernel.ULID][]stacksapi.PullRequest{settings[0].ID: {request}}}
	server.BindPullRequests(reader)
	views, err = copy.views(t.Context(), principal, stacks...)
	if err != nil || len(views) != 2 || reader.calls != 1 || reader.account != account || !reflect.DeepEqual(reader.settings, settings) {
		t.Fatal("late-bound read lost batch/account/settings", views, reader, err)
	}
	got := views[0].PullRequests[0]
	if got.Id != "17" || got.AuthorLogin != "creator" || got.Title != request.Title || got.Url != request.URL || got.HeadBranch != request.HeadBranch ||
		got.HeadBranchURL != request.HeadBranchURL ||
		got.HeadSHA != request.HeadSHA ||
		!got.Draft ||
		got.CreatedAt != int32(created.Unix()) ||
		got.UpdatedAt != int32(request.UpdatedAt.Unix()) ||
		len(got.Runs) != 1 ||
		got.Runs[0].Id != run.String() {
		t.Fatalf("wire metadata/run projection changed: %+v", got)
	} //nolint:gosec // bounded fixture dates fit the served int32 timestamps
	if views[1].PullRequests == nil || len(views[1].PullRequests) != 0 {
		t.Fatal("missing map entry did not produce a JSON list")
	}
	reader.err = errors.New("VCS unavailable")
	if views, err := copy.views(t.Context(), principal, stacks...); !errors.Is(err, reader.err) || views != nil {
		t.Fatal("VCS outage silently became empty requests", views, err)
	}
	reader.calls = 0
	if views, err := copy.views(t.Context(), principal); err != nil || len(views) != 0 || reader.calls != 0 {
		t.Fatal("empty stack list called an unavailable peer", views, err)
	}
}
