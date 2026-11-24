package handover_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"testing"

	cloudapi "github.com/weaveplatform/infraweave/internal/cloud/api"
	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/handover"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// TestUnitScope is the side of its stack's attachments each run type reads
// in each phase: a proposed run's read, a tracked run's read while
// planning and write while applying, or write throughout on a stack that
// deploys automatically, and write throughout for a task, a destroy run
// and a module test run.
func TestUnitScope(t *testing.T) {
	const r, w = cloudapi.ScopeRead, cloudapi.ScopeWrite
	planning := []kernel.RunState{
		kernel.RunStatePreparing, kernel.RunStateInitializing, kernel.RunStatePlanning,
		kernel.RunStatePreparingReplan,
	}
	applying := []kernel.RunState{kernel.RunStatePreparingApply, kernel.RunStateApplying}
	for _, tc := range []struct {
		typ        kernel.RunType
		autodeploy bool
		planning   cloudapi.Scope
		applying   cloudapi.Scope
	}{
		{kernel.RunTypeProposed, false, r, r},
		{kernel.RunTypeProposed, true, r, r},
		{kernel.RunTypeTracked, false, r, w},
		{kernel.RunTypeTracked, true, w, w},
		{kernel.RunTypeTask, false, w, w},
		{kernel.RunTypeDestroy, false, w, w},
		{kernel.RunTypeTesting, false, w, w},
	} {
		for _, s := range planning {
			if got := handover.Scope(tc.typ, s, tc.autodeploy); got != tc.planning {
				t.Errorf("%s in %s, autodeploy %v: %s, want %s", tc.typ, s, tc.autodeploy, got, tc.planning)
			}
		}
		for _, s := range applying {
			if got := handover.Scope(tc.typ, s, tc.autodeploy); got != tc.applying {
				t.Errorf("%s in %s, autodeploy %v: %s, want %s", tc.typ, s, tc.autodeploy, got, tc.applying)
			}
		}
	}
	for _, s := range []kernel.RunState{kernel.RunStatePreparing, kernel.RunStatePerforming} {
		if got := handover.Scope(kernel.RunTypeTask, s, false); got != w {
			t.Errorf("a task in %s: %s", s, got)
		}
	}
	if got := handover.Scope(kernel.RunTypeDestroy, kernel.RunStateDestroying, false); got != w {
		t.Errorf("a destroy run destroying: %s", got)
	}
}

// credentials answers phase, or err, recording each request.
type credentials struct {
	phase cloudapi.Phase
	err   error
	asked []cloudapi.PhaseRequest
}

func (c *credentials) ForPhase(_ context.Context, r cloudapi.PhaseRequest) (cloudapi.Phase, error) {
	c.asked = append(c.asked, r)
	return c.phase, c.err
}

// awsPhase is the phase of an integration the server assumes.
func awsPhase() cloudapi.Phase {
	return cloudapi.Phase{Environment: []cloudapi.Variable{
		{Name: cloudapi.AWSAccessKeyID, Value: "ASIAKEY", Secret: true},
		{Name: cloudapi.AWSSecretAccessKey, Value: "s3cret-key", Secret: true},
		{Name: cloudapi.AWSSecurityToken, Value: "t0ken", Secret: true},
		{Name: cloudapi.AWSSessionToken, Value: "t0ken", Secret: true},
		{Name: "AWS_REGION_HINT", Value: "eu-west-1"},
	}}
}

// TestUnitHandoverCloudCredentials checks that a phase's credentials reach
// the specification as write-only variables whose values only the sealed
// part holds, that a stack variable of the same name replaces a computed
// one, that the scope follows the run's type, state and its stack's
// autodeploy, and that a worker's assumptions reach the clear part with no
// credential.
func TestUnitHandoverCloudCredentials(t *testing.T) {
	w := newWorld(t)
	c := &credentials{phase: awsPhase()}
	w.svc.BindCredentials(c)
	w.env.resolved = configapi.Resolved{Elements: []configapi.Element{
		variable(cloudapi.AWSSessionToken, "mine", false),
	}}
	h, err := w.firstHandover(t, w.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.asked, []cloudapi.PhaseRequest{{
		Account: w.run.Account, Stack: w.run.Stack, Run: w.run.ID, Scope: cloudapi.ScopeRead,
	}}) {
		t.Errorf("asked %+v", c.asked)
	}
	byName := map[string]workersapi.Variable{}
	names := make([]string, 0, len(h.Spec.Environment))
	for _, v := range h.Spec.Environment {
		byName[v.Name] = v
		names = append(names, v.Name)
	}
	if !slices.IsSorted(names) {
		t.Errorf("variables %v", names)
	}
	for _, name := range []string{cloudapi.AWSAccessKeyID, cloudapi.AWSSecretAccessKey, cloudapi.AWSSecurityToken} {
		if v, ok := byName[name]; !ok || !v.WriteOnly || v.Value != nil {
			t.Errorf("%s = %+v", name, v)
		}
	}
	if v := byName[cloudapi.AWSSessionToken]; v.WriteOnly || *v.Value != "mine" {
		t.Errorf("the stack's %s = %+v", cloudapi.AWSSessionToken, v)
	}
	if v := byName["AWS_REGION_HINT"]; v.WriteOnly || *v.Value != "eu-west-1" {
		t.Errorf("a clear computed variable = %+v", v)
	}
	var got sealed
	if err := json.Unmarshal(h.Secrets.Reveal(), &got); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(got.Environment, map[string]string{
		cloudapi.AWSAccessKeyID: "ASIAKEY", cloudapi.AWSSecretAccessKey: "s3cret-key", cloudapi.AWSSecurityToken: "t0ken",
	}) {
		t.Errorf("sealed environment = %v", got.Environment)
	}
	if h.Spec.CloudAssumptions != nil {
		t.Errorf("assumptions = %+v", h.Spec.CloudAssumptions)
	}

	c.asked = nil
	w.store.run.State = kernel.RunStatePreparingApply
	if _, err := w.firstHandover(t, w.run.ID); err != nil || c.asked[0].Scope != cloudapi.ScopeWrite {
		t.Errorf("applying asked %+v, %v", c.asked, err)
	}
	c.asked = nil
	w.store.run.State = kernel.RunStatePreparing
	w.stacks.settings.Autodeploy = true
	if _, err := w.firstHandover(t, w.run.ID); err != nil || c.asked[0].Scope != cloudapi.ScopeWrite {
		t.Errorf("planning to deploy automatically asked %+v, %v", c.asked, err)
	}

	integration := kernel.NewULID()
	tags := map[string]string{"run": w.run.ID.String()}
	c.phase = cloudapi.Phase{WorkerAssumptions: []cloudapi.Assumption{{
		Integration: integration, IntegrationName: "Deploy", RoleARN: "arn:aws:iam::222222222222:role/deploy",
		ExternalID: "ours", SessionName: "s", DurationSeconds: 7200, Region: "eu-west-1", Tags: tags,
	}}}
	h, err = w.firstHandover(t, w.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []workersapi.CloudAssumption{{
		Integration: integration, IntegrationName: "Deploy", RoleARN: "arn:aws:iam::222222222222:role/deploy",
		ExternalID: "ours", SessionName: "s", DurationSeconds: 7200, Region: "eu-west-1", Tags: tags,
	}}
	if len(h.Spec.CloudAssumptions) != 1 || !maps.Equal(h.Spec.CloudAssumptions[0].Tags, tags) ||
		h.Spec.CloudAssumptions[0].Integration != want[0].Integration ||
		h.Spec.CloudAssumptions[0].ExternalID != "ours" || h.Spec.CloudAssumptions[0].DurationSeconds != 7200 {
		t.Errorf("assumptions = %+v", h.Spec.CloudAssumptions)
	}
	var worker sealed
	if err := json.Unmarshal(h.Secrets.Reveal(), &worker); err != nil {
		t.Fatal(err)
	}
	for name := range worker.Environment {
		if name != cloudapi.AWSSessionToken && slices.Contains(cloudapi.AWSVariables(), name) {
			t.Errorf("a worker's assumption sealed %s", name)
		}
	}
}

// TestUnitHandoverCloudRefused checks that an assumption the cloud domain
// refuses fails the handover with its refusal, before the source's
// credential or the in-run API token is minted, and that a service with
// no credentials bound hands over none.
func TestUnitHandoverCloudRefused(t *testing.T) {
	w := newWorld(t)
	refused := &kernel.Invalid{Detail: "AWS integration Deploy could not assume role r: AccessDenied"}
	w.svc.BindCredentials(&credentials{err: refused})
	if _, err := w.firstHandover(t, w.run.ID); !errors.Is(err, refused) {
		t.Fatalf("err = %v", err)
	}
	if len(w.sources.asked) != 0 || len(w.tokens.got) != 0 {
		t.Fatalf("a refused handover minted the source's %v and tokens %v", w.sources.asked, w.tokens.got)
	}

	w = newWorld(t)
	h, err := w.firstHandover(t, w.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range h.Spec.Environment {
		if slices.Contains(cloudapi.AWSVariables(), v.Name) {
			t.Errorf("an unbound handover set %s", v.Name)
		}
	}
}
