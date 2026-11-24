package core

import (
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestUnitPhaseAssumption holds the assumption of each kind of
// integration to its golden values: the side's external ID, the legacy
// one for both sides, a worker's custom one over either, the run's session
// name cut at 64 characters, the duration and region, and the three
// session tags only when the integration tags its sessions.
func TestUnitPhaseAssumption(t *testing.T) {
	id := mustULID(t, "01JC0000000000000000000000")
	run := mustULID(t, "01JC0000000000000000000001")
	region := "eu-west-1"
	base := Integration{ID: id, RoleARN: "arn:aws:iam::222222222222:role/deploy", Duration: 1800}
	web := stacksapi.Stack{Slug: "web"}
	long := stacksapi.Stack{Slug: strings.Repeat("s", 40)}
	for _, tc := range []struct {
		name     string
		i        func(Integration) Integration
		st       stacksapi.Stack
		side     Side
		external string
		session  string
		region   string
		tags     map[string]string
	}{
		{
			name: "read", i: func(i Integration) Integration { return i }, st: web, side: Read,
			external: "acme@01JC0000000000000000000000@web@read",
			session:  "01JC0000000000000000000001@web@acme",
		},
		{
			name: "write, tagged, in a region", st: web, side: Write,
			i: func(i Integration) Integration {
				i.TagSession, i.Region = true, &region
				return i
			},
			external: "acme@01JC0000000000000000000000@web@write",
			session:  "01JC0000000000000000000001@web@acme", region: region,
			tags: map[string]string{TagRun: run.String(), TagStack: "web", TagSpace: "app"},
		},
		{
			name: "legacy", st: web, side: Write,
			i: func(i Integration) Integration {
				i.Legacy = true
				return i
			},
			external: "acme@web", session: "01JC0000000000000000000001@web@acme",
		},
		{
			name: "worker with a custom external ID", st: web, side: Read,
			i: func(i Integration) Integration {
				i.Worker, i.ExternalID = true, "ours"
				return i
			},
			external: "ours", session: "01JC0000000000000000000001@web@acme",
		},
		{
			name: "worker with none", st: web, side: Read,
			i: func(i Integration) Integration {
				i.Worker = true
				return i
			},
			external: "acme@01JC0000000000000000000000@web@read",
			session:  "01JC0000000000000000000001@web@acme",
		},
		{
			name: "long slug", i: func(i Integration) Integration { return i }, st: long, side: Read,
			external: "acme@01JC0000000000000000000000@" + long.Slug + "@read",
			session:  "01JC0000000000000000000001@" + long.Slug[:MaxSessionName-27],
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := PhaseAssumption(tc.i(base), tc.st, "app", "acme", run, tc.side)
			if a.RoleARN != base.RoleARN || a.ExternalID != tc.external || a.SessionName != tc.session ||
				len(a.SessionName) > MaxSessionName || a.Duration != 30*time.Minute || a.Region != tc.region ||
				!maps.Equal(a.Tags, tc.tags) {
				t.Fatalf("assumption = %+v", a)
			}
		})
	}
}

// TestUnitAssumeFailed holds each failure of an assumption at a handover
// to the detail the run fails with: the integration, the role and STS's
// code, the trust text for a refusal, and an unknown failure as it is.
func TestUnitAssumeFailed(t *testing.T) {
	i := Integration{Name: "Deploy", RoleARN: "arn:aws:iam::222222222222:role/deploy"}
	prefix := "AWS integration Deploy could not assume role arn:aws:iam::222222222222:role/deploy: "
	broken := errors.New("broken")
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&Refused{Message: "User: x is not authorized"}, prefix + "AccessDenied: " + TrustDetail + "."},
		{&Upstream{Code: "Throttling", Err: broken}, prefix + "AWS STS failed: Throttling."},
		{&Upstream{Err: broken}, prefix + "AWS STS failed: no answer."},
		{ErrUnsupported, prefix + "the installation holds no AWS credentials."},
	} {
		inv, ok := errors.AsType[*kernel.Invalid](assumeFailed(i, tc.err))
		if !ok || inv.Detail != tc.want || strings.Contains(inv.Detail, "not authorized") {
			t.Errorf("%v: %v, want %q", tc.err, inv, tc.want)
		}
	}
	if err := assumeFailed(i, broken); !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
}
