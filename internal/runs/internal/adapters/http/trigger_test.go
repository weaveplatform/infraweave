package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/trigger"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestUnitTriggerInput reads a trigger's body: none is the defaults, each
// field carries over, the runtime configuration as JSON, and a yaml set
// beside other properties is refused at its pointer.
func TestUnitTriggerInput(t *testing.T) {
	if in, err := input(nil); err != nil || in.Type != "" || in.RuntimeConfig != nil {
		t.Errorf("no body = %+v, %v", in, err)
	}
	typ, sha, force := api.RunType("PROPOSED"), "abc", api.ForceApplyMode("CASCADE")
	image, yaml := "custom", "version: 1"
	in, err := input(&api.RunTriggerRequest{
		RunType: &typ, CommitSha: &sha, ForceApply: &force,
		RuntimeConfig: &api.RuntimeConfigInput{RunnerImage: &image},
	})
	if err != nil || in.Type != kernel.RunTypeProposed || in.CommitSHA != "abc" ||
		in.ForceApply != "CASCADE" || string(in.RuntimeConfig) != `{"runnerImage":"custom"}` {
		t.Errorf("a full body = %+v, %v", in, err)
	}
	if _, err := input(&api.RunTriggerRequest{
		RuntimeConfig: &api.RuntimeConfigInput{Yaml: &yaml},
	}); err != nil {
		t.Errorf("yaml alone = %v", err)
	}
	_, err = input(&api.RunTriggerRequest{
		RuntimeConfig: &api.RuntimeConfigInput{Yaml: &yaml, RunnerImage: &image},
	})
	if inv, ok := errors.AsType[*kernel.Invalid](err); !ok ||
		inv.Errors[0].Pointer != "/runtimeConfig/yaml" {
		t.Errorf("yaml beside a property = %v", err)
	}
}

// TestUnitTriggerErrors answers each refusal of the run operations with
// its problem and passes any other error through.
func TestUnitTriggerErrors(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		slug   string
	}{
		{&lifecycle.Locked{By: "bob"}, http.StatusForbidden, "forbidden"},
		{lifecycle.ErrDisabled, http.StatusConflict, "invalid-state"},
		{trigger.ErrNoCommit, http.StatusConflict, "invalid-state"},
		{stacksapi.ErrNotFound, http.StatusNotFound, "not-found"},
		{read.ErrNotFound, http.StatusNotFound, "not-found"},
		{control.ErrBlocking, http.StatusForbidden, "forbidden"},
		{control.ErrState, http.StatusConflict, "invalid-state"},
		{control.ErrNoHolder, http.StatusNotFound, "not-found"},
	} {
		var p *problem.Error
		if !errors.As(runError(c.err, "infra", ""), &p) || p.Problem.Status != c.status ||
			p.Problem.Type != problem.TypePrefix+c.slug {
			t.Errorf("%v = %+v, want %d %s", c.err, p, c.status, c.slug)
		}
	}
	other := errors.New("other")
	if got := runError(other, "infra", ""); !errors.Is(got, other) {
		t.Errorf("another error = %v", got)
	}
}

// TestUnitTriggerRun answers a run with its history oldest first, its
// until, its force-apply mode and who triggered it, and none of the
// optional ones when it has none.
func TestUnitTriggerRun(t *testing.T) {
	at := time.Unix(1_800_000_000, 0).UTC()
	alice, note := "alice", "pinned"
	v := read.View{
		Run: lifecycle.Run{
			ID: kernel.NewULID(), Type: kernel.RunTypeTracked, State: kernel.RunStateFinished,
			Commit: stacksapi.Commit{
				Hash: "abc", Message: "Add a subnet", Timestamp: at, Branch: "main",
			},
			TriggeredBy: "alice", Priority: kernel.RunPriorityNormal, ForceApply: "SINGLE",
			RuntimeConfig: json.RawMessage(`{}`), CreatedAt: at, UpdatedAt: at.Add(time.Minute),
		},
		Stack: stacksapi.Stack{Slug: "infra"},
		History: []lifecycle.Step{
			{Sequence: 1, State: kernel.RunStateQueued, At: at, Username: &alice},
			{Sequence: 2, State: kernel.RunStateFinished, At: at, Until: &at, Note: &note},
		},
	}
	r := runOf(v)
	if r.Id != v.Run.ID.String() || r.WorkflowId != r.Id || r.StackId != "infra" ||
		r.Branch != "main" || r.Title != "Add a subnet" || !r.Finished ||
		r.Commits[0] != "abc" || *r.ForceApplyMode != "SINGLE" || *r.TriggeredBy != "alice" ||
		r.CreatedAt != 1_800_000_000 || r.UpdatedAt != 1_800_000_060 {
		t.Errorf("run %+v", r)
	}
	if len(r.History) != 2 || r.History[0].State != "QUEUED" || r.History[1].Terminal != true ||
		*r.History[1].Until != 1_800_000_000 || *r.History[1].StateVersion != 2 ||
		r.History[0].Until != nil {
		t.Errorf("history %+v", r.History)
	}
	v.Run.ForceApply, v.Run.TriggeredBy = "", ""
	if r := runOf(v); r.ForceApplyMode != nil || r.TriggeredBy != nil {
		t.Errorf("a run with neither = %+v", r)
	}
}

// TestUnitTriggerRefusals answers a request with no principal 401, and a
// body the trigger refuses before it runs with its problem.
func TestUnitTriggerRefusals(t *testing.T) {
	s := New(nil, nil, nil)
	if _, err := s.RunTrigger(context.Background(), api.RunTriggerRequestObject{}); err == nil {
		t.Error("a request with no principal passed")
	}
	yaml, image := "version: 1", "custom"
	ctx := kernel.WithPrincipal(context.Background(), kernel.Principal{})
	_, err := s.RunTrigger(ctx, api.RunTriggerRequestObject{Body: &api.RunTriggerRequest{
		RuntimeConfig: &api.RuntimeConfigInput{Yaml: &yaml, RunnerImage: &image},
	}})
	if _, ok := errors.AsType[*kernel.Invalid](err); !ok {
		t.Errorf("a refused body = %v", err)
	}
}
