package pg_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/mask"
)

// TestAccRankedExpressionSources exercises service reads over the PostgreSQL
// attachment order, which is deliberately different from source rank. Samples
// and saved templates must use the same winner and secrecy as run resolution.
func TestAccRankedExpressionSources(t *testing.T) {
	for _, tc := range []struct {
		name       string
		contexts   []core.Input
		wantSource string
		wantOwner  string
		wantHooks  []string
		stackOwn   bool
		rawLoser   bool
	}{
		{
			name: "direct before automatic",
			contexts: []core.Input{
				{Name: "Automatic", Labels: []string{"autoattach:*"}},
				{Name: "Direct", Stacks: []core.StackInput{{Stack: "web", Priority: 5}}},
			},
			wantSource: configapi.SourceContext, wantOwner: "direct",
			wantHooks: []string{"Direct", "Automatic", "stack"}, rawLoser: true,
		},
		{
			name: "automatic descending name before creation ID",
			contexts: []core.Input{
				{Name: "Alpha", Labels: []string{"autoattach:*"}},
				{Name: "Zulu", Labels: []string{"autoattach:*"}},
			},
			wantSource: configapi.SourceAutoattached, wantOwner: "zulu",
			wantHooks: []string{"Zulu", "Alpha", "stack"}, rawLoser: true,
		},
		{
			name: "direct ascending priority",
			contexts: []core.Input{
				{Name: "High", Stacks: []core.StackInput{{Stack: "web", Priority: 9}}},
				{Name: "Low", Stacks: []core.StackInput{{Stack: "web", Priority: 5}}},
			},
			wantSource: configapi.SourceContext, wantOwner: "low",
			wantHooks: []string{"Low", "High", "stack"},
		},
		{
			name: "direct slug before creation ID at equal priority",
			contexts: []core.Input{
				{Name: "Zulu", Stacks: []core.StackInput{{Stack: "web", Priority: 5}}},
				{Name: "Alpha", Stacks: []core.StackInput{{Stack: "web", Priority: 5}}},
			},
			wantSource: configapi.SourceContext, wantOwner: "alpha",
			wantHooks: []string{"Alpha", "Zulu", "stack"}, rawLoser: true,
		},
		{
			name: "stack before contexts",
			contexts: []core.Input{
				{Name: "Automatic", Labels: []string{"autoattach:*"}},
				{Name: "Direct", Stacks: []core.StackInput{{Stack: "web", Priority: 5}}},
			},
			wantSource: configapi.SourceStack,
			wantHooks:  []string{"Direct", "Automatic", "stack"}, stackOwn: true,
		},
	} {
		for _, secret := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/secret=%t", tc.name, secret), func(t *testing.T) {
				f := newFixture(t)
				ctx := t.Context()
				f.stacks.hooks["web"] = map[string][]string{
					"before_plan": {"stack"}, "after_plan": {"stack"},
				}
				wantValue := tc.contexts[1].Name
				if tc.stackOwn {
					wantValue = "stack"
					if _, _, err := f.svc.SetStackElement(ctx, f.admin, "web", core.ElementInput{
						Name: "LEVEL", Value: new(wantValue), WriteOnly: secret,
					}, "", "/configId"); err != nil {
						t.Fatal(err)
					}
				}
				created := make([]core.Context, 0, len(tc.contexts))
				for i, in := range tc.contexts {
					writeOnly := !secret
					if i == 1 && !tc.stackOwn {
						writeOnly = secret
					}
					in.Config = []core.ElementInput{{Name: "LEVEL", Value: new(in.Name), WriteOnly: writeOnly}}
					in.Hooks = map[string][]string{"before_plan": {in.Name}, "after_plan": {in.Name}}
					c, err := f.svc.Create(ctx, f.admin, in)
					if err != nil {
						t.Fatal(err)
					}
					created = append(created, c)
				}
				if tc.rawLoser {
					raw, err := f.store.Attachments(ctx, f.acct, []kernel.ULID{f.web.ID}, true)
					if err != nil || len(raw[f.web.ID]) != 2 || raw[f.web.ID][0].Context != created[0].ID {
						t.Fatalf("fixture must place the losing source first in the raw read: %v, %v", raw, err)
					}
				}
				run, err := f.svc.Resolve(ctx, f.acct, f.web.ID)
				if err != nil || len(run.Elements) != 1 {
					t.Fatalf("Resolve = %+v, %v", run, err)
				}
				e := run.Elements[0]
				if e.Source != tc.wantSource || e.Context != tc.wantOwner ||
					e.WriteOnly != secret || string(e.Value.Reveal()) != wantValue {
					t.Errorf("run winner = %+v", e)
				}
				after := slices.Clone(tc.wantHooks)
				slices.Reverse(after)
				if !reflect.DeepEqual(run.Hooks, map[string][]string{"before_plan": tc.wantHooks, "after_plan": after}) {
					t.Errorf("hooks = %v", run.Hooks)
				}
				sample, err := f.svc.Surface().Env(ctx, f.reader, "web")
				if err != nil {
					t.Fatal(err)
				}
				wantSample := wantValue
				if secret {
					wantSample = mask.Replacement
				}
				if got := sample.Sample["env"]; !reflect.DeepEqual(got, map[string]any{"LEVEL": wantSample}) {
					t.Errorf("sample = %v, want LEVEL=%s", got, wantSample)
				}
				derived, _, err := f.svc.SetStackElement(ctx, f.admin, "web", variable("ECHO", "${{ env.LEVEL }}"), "", "/configId")
				if err != nil {
					t.Fatal(err)
				}
				if derived.WriteOnly != secret || (derived.Value == nil) != secret {
					t.Errorf("saved template secrecy = %+v, want writeOnly=%t", derived, secret)
				}
				stored, err := f.svc.StackElement(ctx, f.acct, "web", "ECHO")
				if err != nil || stored.WriteOnly != secret || (stored.Value == nil) != secret {
					t.Errorf("read template secrecy = %+v, %v", stored, err)
				}
				if !secret && (stored.Value == nil || *stored.Value != "${{ env.LEVEL }}") {
					t.Errorf("public template = %+v", stored)
				}
			})
		}
	}
}
