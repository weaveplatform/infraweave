package http

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/ignored"
)

type ignoredViewStore struct {
	ignored.Store
	rows search.Result[ignored.Record]
	err  error
}

func (s ignoredViewStore) SearchIgnored(context.Context, ignored.Scope, search.Input) (search.Result[ignored.Record], error) {
	return s.rows, s.err
}

type ignoredViewStack struct {
	stacksapi.Stacks
	st  stacksapi.Stack
	err error
}

func (s ignoredViewStack) BySlug(context.Context, kernel.AccountID, string) (stacksapi.Stack, error) {
	return s.st, s.err
}

type ignoredViewAuthz struct {
	accessapi.Authorizer
	admin bool
}

func (a ignoredViewAuthz) Authorize(_ context.Context, _ kernel.Principal, action accessapi.Action, _ kernel.ULID) (bool, error) {
	return action == accessapi.ActionSpaceRead || a.admin, nil
}

func TestUnitIgnoredTriggerView(t *testing.T) {
	account := kernel.NewAccountID()
	stack := kernel.NewULID()
	at := time.Unix(1700000000, 0)
	id := kernel.NewULID()
	cause := errors.New("search unavailable")
	req := api.StackSearchRunIgnoredTriggersRequestObject{StackId: "infra"}
	for _, fault := range []string{"none", "principal", "unbound", "missing", "admin", "storage"} {
		t.Run(fault, func(t *testing.T) {
			scope := ignoredViewStack{st: stacksapi.Stack{Account: account, ID: stack, Space: account.ULID}}
			store := ignoredViewStore{
				rows: search.Result[ignored.Record]{
					Edges: []search.Edge[ignored.Record]{
						{
							Cursor: "edge",
							Node: ignored.Record{
								ID:        id,
								CreatedAt: at,
								Reason:    ignored.TagsIgnored,
								Commit: stacksapi.Commit{
									Hash:        "hash",
									Tag:         "v1",
									URL:         "https://example.com/commit",
									Message:     "subject",
									AuthorName:  "Author",
									AuthorLogin: new("login"),
									Timestamp:   at,
								},
							},
						},
					},
					End:     "end",
					HasNext: true,
				},
			}
			if fault == "missing" {
				scope.err = stacksapi.ErrNotFound
			}
			if fault == "storage" {
				store.err = cause
			}
			srv := Server{}.WithIgnored(ignored.New(ignored.Deps{Store: store, Stacks: scope, Authz: ignoredViewAuthz{admin: fault != "admin"}}))
			ctx := kernel.WithPrincipal(t.Context(), kernel.Principal{AccountID: account})
			if fault == "principal" {
				ctx = t.Context()
			}
			if fault == "unbound" {
				srv = Server{}
			}
			response, err := srv.StackSearchRunIgnoredTriggers(ctx, req)
			if fault != "none" {
				if err == nil {
					t.Fatal("refusal returned success", fault)
				}
				if fault == "storage" && !errors.Is(err, cause) {
					t.Fatal("dependency identity lost", err)
				}
				if fault == "unbound" && !errors.Is(err, kernel.ErrUnavailable) {
					t.Fatal(err)
				}
				if fault == "missing" || fault == "admin" {
					p, ok := errors.AsType[*problem.Error](err)
					want := 404
					if fault == "admin" {
						want = 403
					}
					if !ok || p.Problem.Status != want {
						t.Fatal("wrong public refusal", err)
					}
				}
				return
			}
			out, ok := response.(api.StackSearchRunIgnoredTriggers200JSONResponse)
			if err != nil || !ok || len(out.Body.Edges) != 1 {
				t.Fatal(response, err)
			}
			r := out.Body.Edges[0]
			if r.Cursor != "edge" || r.Node.Id != id.String() || r.Node.CreatedAt != int32(at.Unix()) ||
				r.Node.ReasonType != api.RunIgnoredTriggerReasonType(ignored.TagsIgnored) ||
				r.Node.PolicyAttachments == nil ||
				len(r.Node.PolicyAttachments) != 0 ||
				r.Node.Commit.Tag != "v1" ||
				r.Node.Commit.Url != "https://example.com/commit" ||
				r.Node.Commit.AuthorLogin == nil ||
				*r.Node.Commit.AuthorLogin != "login" ||
				r.Node.Commit.Timestamp != int32(at.Unix()) {
				t.Fatal("served projection lost facts or empty policy list", r)
			}
		})
	}
	if ignoredUnix(time.Unix(math.MaxInt32+1, 0)) != math.MaxInt32 || ignoredUnix(time.Unix(math.MinInt32-1, 0)) != math.MinInt32 {
		t.Fatal("timestamp overflows served range")
	}
}
