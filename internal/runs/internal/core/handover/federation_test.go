package handover_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/handover"
)

var update = flag.Bool("update", false, "rewrite golden files")

// features admits every feature but those refused, or fails with err,
// recording the organization and feature of each check.
type features struct {
	refused map[string]bool
	err     error
	asked   []string
}

func (f *features) AllowAccount(_ context.Context, org kernel.OrganizationID, _ kernel.AccountID, feature string) error {
	f.asked = append(f.asked, org.String()+" "+feature)
	if f.refused[feature] {
		return &featuresapi.Refusal{Feature: feature, Tiers: []string{"Starter"}}
	}
	return f.err
}

// federated is w's service again, with f as its features and org as
// every account's organization, or orgErr.
func federated(t *testing.T, w *world, f *features, org kernel.OrganizationID, orgErr error) {
	t.Helper()
	w.svc = handover.New(handover.Deps{
		Store: w.store, Stacks: w.stacks, Spaces: w.spaces, Accounts: w.accounts,
		Environment: w.env, Tokens: w.tokens, Catalog: w.catalog, Objects: w.objects,
		Features: f,
		Organization: func(context.Context, kernel.AccountID) (kernel.OrganizationID, error) {
			return org, orgErr
		},
	})
	w.svc.BindExpressions(registry(t))
	w.svc.BindSources(w.sources)
}

// golden compares v, as indented JSON, with testdata/name, which -update
// rewrites.
func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s:\n%s\nwant\n%s", name, got, want)
	}
}

// federationCase is one handover's federation request, without the IDs
// each world makes afresh, and the sealed token it put.
type federationCase struct {
	RunType    kernel.RunType  `json:"runType"`
	State      kernel.RunState `json:"state"`
	Autodeploy bool            `json:"autodeploy"`
	CallerType string          `json:"callerType"`
	Caller     string          `json:"caller"`
	Phase      kernel.RunState `json:"phase"`
	Sealed     string          `json:"sealed"`
	ExpiresAt  int64           `json:"expiresAt"`
}

// TestUnitHandoverFederationToken checks the federation request of each
// handover that starts a phase, golden: a stack caller in the stack's
// space, the run's type and the state whose handover starts the phase,
// and the stack's autodeploy, with the token and its expiry in the sealed
// part beside the in-run API token. A parse run receives none.
func TestUnitHandoverFederationToken(t *testing.T) {
	org := kernel.NewOrganizationID()
	handovers := []struct {
		typ        kernel.RunType
		state      kernel.RunState
		autodeploy bool
	}{
		{kernel.RunTypeProposed, kernel.RunStatePreparing, false},
		{kernel.RunTypeTracked, kernel.RunStatePreparing, false},
		{kernel.RunTypeTracked, kernel.RunStatePreparingApply, false},
		{kernel.RunTypeTracked, kernel.RunStatePreparingReplan, false},
		{kernel.RunTypeTracked, kernel.RunStatePreparing, true},
		{kernel.RunTypeTask, kernel.RunStatePreparing, false},
		{kernel.RunTypeDestroy, kernel.RunStatePreparing, false},
		{kernel.RunTypeTesting, kernel.RunStatePreparing, false},
		{kernel.RunTypeParse, kernel.RunStatePreparing, false},
	}
	cases := make([]federationCase, 0, len(handovers))
	for _, tc := range handovers {
		w := newWorld(t)
		f := &features{}
		federated(t, w, f, org, nil)
		w.store.run.Type, w.store.run.State = tc.typ, tc.state
		w.stacks.settings.Autodeploy = tc.autodeploy
		h, err := w.firstHandover(t, w.run.ID)
		if err != nil {
			t.Fatalf("%s in %s: %v", tc.typ, tc.state, err)
		}
		var got sealed
		if err := json.Unmarshal(h.Secrets.Reveal(), &got); err != nil {
			t.Fatal(err)
		}
		c := federationCase{RunType: tc.typ, State: tc.state, Autodeploy: tc.autodeploy}
		if got.OIDCToken != nil {
			c.Sealed, c.ExpiresAt = *got.OIDCToken, *got.OIDCTokenExpiresAt
		}
		switch len(w.tokens.federation) {
		case 0:
		case 1:
			r := w.tokens.federation[0]
			if r.Account != w.run.Account || r.Run != w.run.ID || r.Space != w.space || r.RunType != tc.typ ||
				r.Autodeploy != tc.autodeploy {
				t.Errorf("%s in %s: request %+v", tc.typ, tc.state, r)
			}
			c.CallerType, c.Caller, c.Phase = r.CallerType, r.Caller, r.Phase
			if len(f.asked) != 1 || f.asked[0] != org.String()+" "+featuresapi.OIDCFederation {
				t.Errorf("%s in %s: features asked %v", tc.typ, tc.state, f.asked)
			}
		default:
			t.Errorf("%s in %s: %d federation tokens", tc.typ, tc.state, len(w.tokens.federation))
		}
		if got.APIToken != "in-run" {
			t.Errorf("%s in %s: in-run API token %q", tc.typ, tc.state, got.APIToken)
		}
		cases = append(cases, c)
	}
	golden(t, "federation.json", cases)
}

// TestUnitHandoverFederationGate checks that a run of an account whose
// tier lacks the feature, or of a service with no features bound, goes
// on with neither a token nor a request, and that a failed organization
// read, feature check or mint fails the handover.
func TestUnitHandoverFederationGate(t *testing.T) {
	org := kernel.NewOrganizationID()
	for name, f := range map[string]*features{
		"lacking": {refused: map[string]bool{featuresapi.OIDCFederation: true}},
		"unbound": nil,
	} {
		w := newWorld(t)
		if f != nil {
			federated(t, w, f, org, nil)
		}
		h, err := w.firstHandover(t, w.run.ID)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var got sealed
		if err := json.Unmarshal(h.Secrets.Reveal(), &got); err != nil {
			t.Fatal(err)
		}
		if got.OIDCToken != nil || got.OIDCTokenExpiresAt != nil || len(w.tokens.federation) != 0 {
			t.Errorf("%s: sealed %v, %v; requests %+v", name, got.OIDCToken, got.OIDCTokenExpiresAt, w.tokens.federation)
		}
		for _, v := range h.Spec.Environment {
			if v.Name == kernel.OIDCTokenVariable {
				t.Errorf("%s: the specification names %s", name, v.Name)
			}
		}
	}

	down := errors.New("down")
	for name, set := range map[string]func(w *world){
		"organization": func(w *world) { federated(t, w, &features{}, org, down) },
		"features":     func(w *world) { federated(t, w, &features{err: down}, org, nil) },
		"mint": func(w *world) {
			federated(t, w, &features{}, org, nil)
			w.tokens.federationErr = down
		},
	} {
		w := newWorld(t)
		set(w)
		if _, err := w.firstHandover(t, w.run.ID); !errors.Is(err, down) {
			t.Errorf("%s down: %v", name, err)
		}
	}
}
