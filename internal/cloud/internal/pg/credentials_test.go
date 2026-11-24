package pg_test

import (
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	cloudapi "github.com/weaveplatform/infraweave/internal/cloud/api"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// phase answers the credentials of run's phase on the stack st for scope.
func (f *fixture) phase(t *testing.T, st kernel.ULID, run kernel.ULID, scope cloudapi.Scope) (cloudapi.Phase, error) {
	t.Helper()
	return f.svc.ForPhase(t.Context(), cloudapi.PhaseRequest{Account: f.acct, Stack: st, Run: run, Scope: scope})
}

// TestAccForPhase checks that a phase receives the credentials of the
// attachment that serves its side, assumed now with that side's external
// ID, the run's session name, the integration's duration and region, and
// the session tags of one that tags its sessions; that a side no
// attachment serves receives none; and that a worker's integration is
// answered as the assumption its worker makes, the server assuming
// nothing.
func TestAccForPhase(t *testing.T) {
	f := newFixture(t)
	run := kernel.NewULID()
	for _, scope := range []cloudapi.Scope{cloudapi.ScopeRead, cloudapi.ScopeWrite} {
		if p, err := f.phase(t, f.web.ID, run, scope); err != nil || p.Environment != nil || p.WorkerAssumptions != nil {
			t.Fatalf("%s with no attachment = %+v, %v", scope, p, err)
		}
	}
	reader := f.create(t, "reader", "team", core.Input{
		Region: ptr("eu-west-1"), Duration: ptr[int32](1800), TagSession: true,
	})
	f.attach(t, reader, "web", true, false)
	attaching := len(f.roles.assumed)
	p, err := f.phase(t, f.web.ID, run, cloudapi.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	n := strconv.Itoa(attaching + 1)
	want := []cloudapi.Variable{
		{Name: cloudapi.AWSAccessKeyID, Value: "ASIA" + n, Secret: true},
		{Name: cloudapi.AWSSecretAccessKey, Value: "secret" + n, Secret: true},
		{Name: cloudapi.AWSSecurityToken, Value: "token" + n, Secret: true},
		{Name: cloudapi.AWSSessionToken, Value: "token" + n, Secret: true},
	}
	if !slices.Equal(p.Environment, want) || p.WorkerAssumptions != nil {
		t.Fatalf("read = %+v", p)
	}
	got := f.roles.assumed[attaching]
	if got.RoleARN != reader.RoleARN || got.ExternalID != core.ExternalID("acme", reader.ID, "web", core.Read) ||
		got.SessionName != core.SessionName(run, "web", "acme") || got.Duration != 30*time.Minute ||
		got.Region != "eu-west-1" || !maps.Equal(got.Tags, map[string]string{
		core.TagRun: run.String(), core.TagStack: "web", core.TagSpace: "app",
	}) {
		t.Fatalf("assumed %+v", got)
	}
	if p, err := f.phase(t, f.web.ID, run, cloudapi.ScopeWrite); err != nil || p.Environment != nil {
		t.Fatalf("write with a read attachment alone = %+v, %v", p, err)
	}

	writer := f.create(t, "writer", "", core.Input{})
	f.attach(t, writer, "web", false, true)
	if _, err := f.phase(t, f.web.ID, run, cloudapi.ScopeWrite); err != nil {
		t.Fatal(err)
	}
	got = f.roles.assumed[len(f.roles.assumed)-1]
	if got.RoleARN != writer.RoleARN || got.ExternalID != core.ExternalID("acme", writer.ID, "web", core.Write) ||
		got.Tags != nil || got.Region != "" || got.Duration != time.Hour {
		t.Fatalf("assumed %+v", got)
	}

	worker := f.create(t, "worker", "hidden", core.Input{
		Worker: true, ExternalID: "ours", Duration: ptr[int32](7200), Region: ptr("us-east-2"), TagSession: true,
	})
	f.attach(t, worker, "db", true, true)
	before := len(f.roles.assumed)
	p, err = f.phase(t, f.data.ID, run, cloudapi.ScopeWrite)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.roles.assumed) != before || p.Environment != nil || len(p.WorkerAssumptions) != 1 {
		t.Fatalf("worker = %+v, assumed %d", p, len(f.roles.assumed)-before)
	}
	a := p.WorkerAssumptions[0]
	if a.Integration != worker.ID || a.IntegrationName != "worker" || a.RoleARN != worker.RoleARN ||
		a.ExternalID != "ours" || a.SessionName != core.SessionName(run, "db", "acme") ||
		a.DurationSeconds != 7200 || a.Region != "us-east-2" ||
		!maps.Equal(a.Tags, map[string]string{core.TagRun: run.String(), core.TagStack: "db", core.TagSpace: "hidden"}) {
		t.Fatalf("worker assumption = %+v", a)
	}

	vars, err := f.svc.CredentialVariables(t.Context(), f.acct, []kernel.ULID{f.web.ID, f.data.ID, kernel.NewULID()})
	if err != nil || len(vars) != 2 || !slices.Equal(vars[f.web.ID], cloudapi.AWSVariables()) ||
		!slices.Equal(vars[f.data.ID], cloudapi.AWSVariables()) {
		t.Fatalf("variables = %v, %v", vars, err)
	}
}

// TestAccForPhaseComputedAndLegacy checks that an attachment computed from
// an autoattach label serves its side, and that a stack's legacy AWS role
// is assumed with the legacy external ID for either side.
func TestAccForPhaseComputedAndLegacy(t *testing.T) {
	f := newFixture(t)
	f.stacks.labels["web"] = []string{"prod"}
	run := kernel.NewULID()
	auto := f.create(t, "auto", "team", core.Input{Autoattach: true, Labels: []string{"autoattach_write:prod"}})
	if _, err := f.phase(t, f.web.ID, run, cloudapi.ScopeWrite); err != nil {
		t.Fatal(err)
	}
	if got := f.roles.assumed[len(f.roles.assumed)-1]; got.ExternalID != core.ExternalID("acme", auto.ID, "web", core.Write) {
		t.Fatalf("assumed %+v", got)
	}
	if _, _, err := f.svc.SetLegacy(t.Context(), f.admin, "db", core.LegacyInput{RoleARN: role("legacy")}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []cloudapi.Scope{cloudapi.ScopeRead, cloudapi.ScopeWrite} {
		if _, err := f.phase(t, f.data.ID, run, scope); err != nil {
			t.Fatal(err)
		}
		if got := f.roles.assumed[len(f.roles.assumed)-1]; got.RoleARN != role("legacy") ||
			got.ExternalID != core.LegacyExternalID("acme", "db") {
			t.Fatalf("%s assumed %+v", scope, got)
		}
	}
	vars, err := f.svc.CredentialVariables(t.Context(), f.acct, []kernel.ULID{f.web.ID})
	if err != nil || !slices.Equal(vars[f.web.ID], cloudapi.AWSVariables()) {
		t.Fatalf("variables = %v, %v", vars, err)
	}
}

// TestAccForPhaseRefused checks that a role that refuses the server at a
// handover answers a *kernel.Invalid naming the integration, STS's code
// and the trust text, never STS's message, and that a scope that is
// neither side and each failed read are errors.
func TestAccForPhaseRefused(t *testing.T) {
	f := newFixture(t)
	run := kernel.NewULID()
	i := f.create(t, "deploy", "team", core.Input{})
	f.attach(t, i, "web", true, true)
	f.roles.trusted[i.RoleARN] = nil
	_, err := f.phase(t, f.web.ID, run, cloudapi.ScopeRead)
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok || !strings.Contains(inv.Detail, "AWS integration deploy") || !strings.Contains(inv.Detail, "AccessDenied") ||
		!strings.Contains(inv.Detail, core.TrustDetail) || strings.Contains(inv.Detail, "not authorized") {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.phase(t, f.web.ID, run, "admin"); err == nil {
		t.Fatal("a scope that is neither side succeeded")
	}
	f.roles.err = errBroken
	if _, err := f.phase(t, f.web.ID, run, cloudapi.ScopeRead); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	f.roles.err = nil
	f.accountsBroken(t)
	if _, err := f.phase(t, f.web.ID, run, cloudapi.ScopeRead); !errors.Is(err, errBroken) {
		t.Fatalf("accounts: err = %v", err)
	}
	f.spaces.err = errBroken
	if _, err := f.phase(t, f.web.ID, run, cloudapi.ScopeRead); !errors.Is(err, errBroken) {
		t.Fatalf("spaces: err = %v", err)
	}
	if _, err := f.svc.CredentialVariables(t.Context(), f.acct, []kernel.ULID{f.web.ID}); !errors.Is(err, errBroken) {
		t.Fatalf("variables: err = %v", err)
	}
	f.spaces.err = nil
	f.stacks.err = errBroken
	if _, err := f.phase(t, f.web.ID, run, cloudapi.ScopeRead); !errors.Is(err, errBroken) {
		t.Fatalf("stacks: err = %v", err)
	}
}
