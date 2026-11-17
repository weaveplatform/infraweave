package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
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
// its problem, a stack another caller has locked as resource-locked
// naming the holder, as the stacks domain answers it, and passes any
// other error through.
func TestUnitTriggerErrors(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		slug   string
	}{
		{&lifecycle.Locked{By: "bob"}, http.StatusConflict, "resource-locked"},
		{lifecycle.ErrDisabled, http.StatusConflict, "invalid-state"},
		{lifecycle.ErrDeleting, http.StatusConflict, "invalid-state"},
		{trigger.ErrNoCommit, http.StatusConflict, "invalid-state"},
		{trigger.ErrLocalPreview, http.StatusForbidden, "forbidden"},
		{stacksapi.ErrNotFound, http.StatusNotFound, "not-found"},
		{read.ErrNotFound, http.StatusNotFound, "not-found"},
		{control.ErrBlocking, http.StatusForbidden, "forbidden"},
		{control.ErrState, http.StatusConflict, "invalid-state"},
		{control.ErrNoHolder, http.StatusNotFound, "not-found"},
		{
			&control.Blocked{Action: accessapi.ActionRunStopBlocking},
			http.StatusForbidden, "forbidden",
		},
		{
			&control.Refused{State: kernel.RunStateApplying, Needs: "a stop needs"},
			http.StatusConflict, "invalid-state",
		},
	} {
		var p *problem.Error
		if !errors.As(runError(c.err, "infra", ""), &p) || p.Problem.Status != c.status ||
			p.Problem.Type != problem.TypePrefix+c.slug {
			t.Errorf("%v = %+v, want %d %s", c.err, p, c.status, c.slug)
		}
	}
	var p *problem.Error
	if !errors.As(runError(fmt.Errorf("trigger: %w", &lifecycle.Locked{By: "bob"}), "infra",
		""), &p) || p.Problem.LockedBy != "bob" || p.Problem.Action != "" {
		t.Errorf("a locked stack = %+v", p)
	}
	if !errors.As(runError(&control.Blocked{Action: accessapi.ActionRunKillBlocking}, "infra",
		""), &p) || p.Problem.Action != "RUN_KILL_BLOCKING" {
		t.Errorf("a blocked kill names %q", p.Problem.Action)
	}
	if !errors.As(runError(&control.Refused{State: kernel.RunStateApplying, Needs: "a stop needs"},
		"infra", ""), &p) || p.Problem.Detail != "The run is APPLYING; a stop needs." {
		t.Errorf("a refusal's detail is %q", p.Problem.Detail)
	}
	other := errors.New("other")
	if got := runError(other, "infra", ""); !errors.Is(got, other) {
		t.Errorf("another error = %v", got)
	}
}

// TestUnitTriggerRun answers a run with its history oldest first, its
// until, its force-apply mode, who triggered it and a task's command,
// and none of the optional ones when it has none.
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
		r.CreatedAt != 1_800_000_000 || r.UpdatedAt != 1_800_000_060 || r.Command != nil {
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
	v.Run.Type, v.Run.Task = kernel.RunTypeTask, &kernel.Task{Command: "terraform output"}
	if r := runOf(v); r.Command == nil || *r.Command != "terraform output" || r.Type != "TASK" {
		t.Errorf("a task = %+v", r)
	}
}

// TestUnitTriggerRefusals answers a request with no principal 401, and a
// body the trigger refuses before it runs with its problem.
func TestUnitTriggerRefusals(t *testing.T) {
	s := New(nil, nil, nil, nil, nil)
	if _, err := s.RunTrigger(context.Background(), api.RunTriggerRequestObject{}); err == nil {
		t.Error("a request with no principal passed")
	}
	if _, err := s.UploadLocalWorkspace(context.Background(),
		api.UploadLocalWorkspaceRequestObject{}); err == nil {
		t.Error("an upload with no principal passed")
	}
	if _, err := s.RunProposeLocalWorkspace(context.Background(),
		api.RunProposeLocalWorkspaceRequestObject{}); err == nil {
		t.Error("a proposal with no principal passed")
	}
	yaml, image := "version: 1", "custom"
	ctx := kernel.WithPrincipal(context.Background(), kernel.Principal{})
	_, err := s.RunTrigger(ctx, api.RunTriggerRequestObject{Body: &api.RunTriggerRequest{
		RuntimeConfig: &api.RuntimeConfigInput{Yaml: &yaml, RunnerImage: &image},
	}})
	if _, ok := errors.AsType[*kernel.Invalid](err); !ok {
		t.Errorf("a refused body = %v", err)
	}
	if _, err := s.ProposeWithOverrides(context.Background(),
		api.ProposeWithOverridesRequestObject{}); err == nil {
		t.Error("a proposal with overrides and no principal passed")
	}
	if _, err := s.TaskCreate(context.Background(), api.TaskCreateRequestObject{}); err == nil {
		t.Error("a task with no principal passed")
	}
	if _, err := s.RunTriggerBatch(context.Background(),
		api.RunTriggerBatchRequestObject{}); err == nil {
		t.Error("a batch with no principal passed")
	}
	for _, body := range []*api.RunTriggerBatchInput{nil, {Stacks: []api.RunTriggerBatchStackInput{}}} {
		_, err := s.RunTriggerBatch(ctx, api.RunTriggerBatchRequestObject{Body: body})
		if inv, ok := errors.AsType[*kernel.Invalid](
			err,
		); !ok ||
			inv.Errors[0].Pointer != "/stacks" {
			t.Errorf("a batch with body %+v = %v, want Invalid at /stacks", body, err)
		}
	}
	for _, body := range []*api.TaskCreateRequest{nil, {Command: " "}} {
		_, err := s.TaskCreate(ctx, api.TaskCreateRequestObject{StackId: "infra", Body: body})
		if inv, ok := errors.AsType[*kernel.Invalid](err); !ok ||
			inv.Errors[0].Pointer != "/command" {
			t.Errorf("a task with body %+v = %v, want Invalid at /command", body, err)
		}
	}
	sha := "abc"
	for _, name := range []string{
		"INFRAWEAVE_X", "A=B", "TF_APPEND_USER_AGENT", "TF_VAR_infraweave_api_endpoint",
		"INFRAWEAVE_MAX_REQUESTS_PER_SECOND", "INFRAWEAVE_MAX_REQUESTS_BURST",
		"INFRAWEAVE_SENSITIVE_OUTPUT_UPLOAD_ENABLED", "INFRAWEAVE_VENDOR_BINARY_CACHED",
	} {
		t.Run(name, func(t *testing.T) {
			ovs := &[]api.EnvironmentVariable{{Key: name, Value: "1"}}
			_, err := s.ProposeWithOverrides(ctx, api.ProposeWithOverridesRequestObject{
				Body: &api.ProposeWithOverridesRequest{CommitSha: &sha, EnvironmentVarsOverrides: ovs},
			})
			if inv, ok := errors.AsType[*kernel.Invalid](err); !ok ||
				inv.Errors[0].Pointer != "/environmentVarsOverrides/0/key" {
				t.Errorf("a refused override = %v", err)
			}
			_, err = s.RunProposeLocalWorkspace(ctx, api.RunProposeLocalWorkspaceRequestObject{
				Body: &api.RunProposeLocalWorkspaceRequest{Workspace: "upload", EnvironmentVarsOverrides: ovs},
			})
			if inv, ok := errors.AsType[*kernel.Invalid](err); !ok ||
				inv.Errors[0].Pointer != "/environmentVarsOverrides/0/key" {
				t.Errorf("a refused override of a local workspace = %v", err)
			}
		})
	}
}

// TestUnitOverrides takes a body's overrides in its order, and none from
// a body that names none.
func TestUnitOverrides(t *testing.T) {
	if got := overrides(nil); got != nil {
		t.Errorf("no overrides = %+v", got)
	}
	got := overrides(&[]api.EnvironmentVariable{{Key: "B", Value: "2"}, {Key: "A", Value: "1"}})
	if len(got) != 2 || got[0] != (lifecycle.Override{Name: "B", Value: "2"}) ||
		got[1] != (lifecycle.Override{Name: "A", Value: "1"}) {
		t.Errorf("overrides = %+v", got)
	}
}

// runtimeStacks exposes a stack without a commit: an accepted YAML value
// reaches the ordinary missing-commit refusal; no lifecycle can queue a run.
type runtimeStacks struct {
	stacksapi.Stacks
	vendor string
}

func (s runtimeStacks) BySlug(context.Context, kernel.AccountID, string) (stacksapi.Stack, error) {
	return stacksapi.Stack{}, nil
}

func (s runtimeStacks) Settings(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Settings, error) {
	return stacksapi.Settings{Vendor: s.vendor}, nil
}

type (
	runtimeDB struct{}
	runtimeTx struct{ pgx.Tx }
)

func (runtimeDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return runtimeTx{}, nil }
func (runtimeTx) Commit(context.Context) error                           { return nil }
func (runtimeTx) Rollback(context.Context) error                         { return nil }

// TestUnitTriggerTerragruntYAML refuses malformed YAML and non-booleans
// before queueing, and leaves other vendors' runtime YAML unread.
func TestUnitTriggerTerragruntYAML(t *testing.T) {
	for _, tc := range []struct {
		vendor, yaml string
		refused      bool
	}{
		{"terragrunt", "terragrunt: [", true},
		{"terragrunt", "terragrunt: {}\n---\n[", true},
		{"terragrunt", "terragrunt: {}\n---\nversion: 1", true},
		{"terragrunt", "terragrunt: {use_run_all: \"yes\"}", true},
		{"terragrunt", "terragrunt: {use_run_all: false}", false},
		{"terragrunt", "terragrunt: {}", false},
		{"terraform", "terragrunt: [", false},
		{"opentofu", "terragrunt: {use_run_all: \"yes\"}", false},
		{"opentofu", "terragrunt: {}\n---\n[", false},
	} {
		t.Run(tc.vendor+"/"+tc.yaml, func(t *testing.T) {
			service := trigger.New(trigger.Deps{DB: runtimeDB{}, Stacks: runtimeStacks{vendor: tc.vendor}})
			s := New(service, nil, nil, nil, nil)
			ctx := kernel.WithPrincipal(t.Context(), kernel.Principal{})
			_, err := s.RunTrigger(ctx, api.RunTriggerRequestObject{StackId: "infra", Body: &api.RunTriggerRequest{
				RuntimeConfig: &api.RuntimeConfigInput{Yaml: &tc.yaml},
			}})
			if tc.refused {
				inv, ok := errors.AsType[*kernel.Invalid](err)
				if !ok || len(inv.Errors) != 1 || inv.Errors[0].Pointer != "/runtimeConfig/yaml" {
					t.Fatalf("runtime YAML = %v", err)
				}
				return
			}
			refused, ok := errors.AsType[*problem.Error](err)
			if !ok || refused.Problem.Status != http.StatusConflict ||
				refused.Problem.Type != problem.TypePrefix+"invalid-state" {
				t.Fatalf("accepted YAML must reach the missing-commit refusal: %v", err)
			}
		})
	}
}
