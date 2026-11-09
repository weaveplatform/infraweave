package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/configuration/internal/http"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// handlers are each of the partial's operations on context shared, its
// element A, stack web and attachment att, by operation ID.
func handlers(ctx context.Context, s http.Server, att string) map[string]func() error {
	in := api.ContextInput{Name: "Shared", Space: new("team")}
	element := api.ConfigInput{Id: "B", Type: api.ENVIRONMENTVARIABLE, Value: "2"}
	return map[string]func() error{
		"contexts": func() error { _, err := s.Contexts(ctx, api.ContextsRequestObject{}); return err },
		"attachableContextsInSpace": func() error {
			_, err := s.AttachableContextsInSpace(ctx, api.AttachableContextsInSpaceRequestObject{
				Params: api.AttachableContextsInSpaceParams{Id: "app"},
			})
			return err
		},
		"searchContexts": func() error {
			_, err := s.SearchContexts(
				ctx,
				api.SearchContextsRequestObject{Body: &api.SearchInput{}},
			)
			return err
		},
		"searchContextsSuggestions": func() error {
			_, err := s.SearchContextsSuggestions(ctx, api.SearchContextsSuggestionsRequestObject{
				Body: &api.SearchSuggestionsInput{Fields: &[]string{"label"}},
			})
			return err
		},
		"searchSpaceContexts": func() error {
			_, err := s.SearchSpaceContexts(ctx, api.SearchSpaceContextsRequestObject{
				Body: &api.SearchSpaceContextsRequest{Space: "app"},
			})
			return err
		},
		"searchSpaceContextsSuggestions": func() error {
			_, err := s.SearchSpaceContextsSuggestions(
				ctx,
				api.SearchSpaceContextsSuggestionsRequestObject{
					Body: &api.SearchSpaceContextsSuggestionsRequest{
						Space: "app", Input: api.SearchSuggestionsInput{Fields: &[]string{"label"}},
					},
				},
			)
			return err
		},
		"context": func() error {
			_, err := s.Context(ctx, api.ContextRequestObject{ContextId: "shared"})
			return err
		},
		"contextCreateV2": func() error {
			other := api.ContextInput{Name: "Other", Space: new("team")}
			_, err := s.ContextCreateV2(ctx, api.ContextCreateV2RequestObject{Body: &other})
			return err
		},
		"contextUpdateV2": func() error {
			_, err := s.ContextUpdateV2(ctx, api.ContextUpdateV2RequestObject{
				ContextId: "shared", Body: &api.ContextUpdateV2Request{Input: in},
			})
			return err
		},
		"contextPatch": func() error {
			_, err := s.ContextPatch(ctx, api.ContextPatchRequestObject{
				ContextId: "shared",
				Body:      &api.MergePatch{"input": map[string]any{"description": "x"}},
			})
			return err
		},
		"contextDelete": func() error {
			_, err := s.ContextDelete(ctx, api.ContextDeleteRequestObject{
				ContextId: "shared", Params: api.ContextDeleteParams{Force: new(true)},
			})
			return err
		},
		"contextAttachedStack": func() error {
			_, err := s.ContextAttachedStack(ctx, api.ContextAttachedStackRequestObject{
				ContextId: "shared", Params: api.ContextAttachedStackParams{Id: att},
			})
			return err
		},
		"contextConfigElement": func() error {
			_, err := s.ContextConfigElement(ctx, api.ContextConfigElementRequestObject{
				ContextId: "shared", Params: api.ContextConfigElementParams{Id: "A"},
			})
			return err
		},
		"contextConfigAdd": func() error {
			_, err := s.ContextConfigAdd(ctx, api.ContextConfigAddRequestObject{
				ContextId: "shared", Body: &element,
			})
			return err
		},
		"contextConfigSet": func() error {
			_, err := s.ContextConfigSet(ctx, api.ContextConfigSetRequestObject{
				ContextId: "shared", ConfigId: "B",
				Body: &api.ConfigElementInput{Type: api.ENVIRONMENTVARIABLE, Value: new("2")},
			})
			return err
		},
		"contextConfigDelete": func() error {
			_, err := s.ContextConfigDelete(ctx, api.ContextConfigDeleteRequestObject{
				ContextId: "shared", ConfigId: "A",
			})
			return err
		},
		"contextAttach": func() error {
			_, err := s.ContextAttach(ctx, api.ContextAttachRequestObject{
				StackId: "web", ContextId: "shared", Body: &api.ContextAttachRequest{Priority: 1},
			})
			return err
		},
		"contextDetach": func() error {
			_, err := s.ContextDetach(ctx, api.ContextDetachRequestObject{AttachmentId: att})
			return err
		},
	}
}

// TestAccHandlers checks that every operation refuses a request with no
// principal, that one the service denies answers 403 with the action,
// and that every operation fails, and not as a missing entity, when the
// spaces or the database are down.
func TestAccHandlers(t *testing.T) {
	f := newFixture(t)
	s := http.New(f.svc, f.authz, f.spaces)
	f.create(t, "Shared", "team", variable("A", "1"))
	a, _, err := f.svc.Attach(t.Context(), f.admin, "shared", "web", 0)
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range handlers(context.Background(), s, a.ID.String()) {
		if err := call(); !isProblem(err, 401) {
			t.Errorf("%s with no principal = %v", name, err)
		}
	}

	_, err = s.ContextUpdateV2(
		kernel.WithPrincipal(t.Context(), f.writer),
		api.ContextUpdateV2RequestObject{
			ContextId: "shared", Body: &api.ContextUpdateV2Request{
				Input: api.ContextInput{Name: "Shared", Space: new("app")},
			},
		},
	)
	if pe, ok := errors.AsType[*problem.Error](err); !ok || pe.Problem.Status != 403 ||
		pe.Problem.Action != "CONTEXT_CREATE" {
		t.Errorf("a move the writer may not make = %v", err)
	}

	ctx := kernel.WithPrincipal(t.Context(), f.admin)
	calls := handlers(ctx, s, a.ID.String())
	f.spaces.err = errors.New("down")
	for _, name := range []string{
		"contexts", "attachableContextsInSpace", "searchContexts", "searchContextsSuggestions",
		"searchSpaceContexts", "searchSpaceContextsSuggestions", "context", "contextCreateV2",
		"contextUpdateV2", "contextPatch", "contextDelete", "contextAttachedStack", "contextAttach",
	} {
		if err := calls[name](); err == nil || isProblem(err, 404) {
			t.Errorf("%s with the spaces down = %v", name, err)
		}
	}
	f.spaces.err = nil
	if err := calls["contextDetach"](); err != nil {
		t.Fatal(err)
	}
	f.spaces.err = errors.New("down")
	if _, _, err := f.svc.Attach(t.Context(), f.admin, "shared", "web", 0); err == nil {
		t.Error("an attach with the spaces down succeeded")
	}
	f.spaces.err = nil

	f.db.Close()
	for name, call := range calls {
		if err := call(); err == nil || isProblem(err, 404) {
			t.Errorf("%s on a closed database = %v", name, err)
		}
	}
}

// isProblem reports whether err is a problem answered with status.
func isProblem(err error, status int) bool {
	pe, ok := errors.AsType[*problem.Error](err)
	return ok && pe.Problem.Status == status
}
