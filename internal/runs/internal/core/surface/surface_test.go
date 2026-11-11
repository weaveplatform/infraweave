package surface_test

import (
	"context"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/surface"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestUnitSurfaceRun checks a template against run: a misspelt field of
// the run or its commit is an error at its range, and a run's Value
// renders each field the schema declares.
func TestUnitSurfaceRun(t *testing.T) {
	r, err := expr.NewRegistry(expr.Defaults(), expr.Surface{
		Name: "environment", Variables: []expr.Variable{surface.Variable()},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := func(source string) expr.Request {
		return expr.Request{Surface: "environment", Form: expr.FormTemplate, Source: source}
	}
	for _, c := range []struct {
		source   string
		from, to int
	}{
		{"${{ run.nope }}", 9, 13},
		{"${{ run.commit.sha }}", 16, 19},
	} {
		chk, err := r.Check(request(c.source))
		if err != nil || len(chk.Diagnostics) != 1 ||
			chk.Diagnostics[0].Range.Start.Column != c.from ||
			chk.Diagnostics[0].Range.End.Column != c.to {
			t.Errorf("%s = %+v, %v", c.source, chk.Diagnostics, err)
		}
	}
	run := lifecycle.Run{
		ID: kernel.NewULID(), Type: kernel.RunTypeProposed, TriggeredBy: "alice",
		Priority: kernel.RunPriorityLow,
		Commit:   stacksapi.Commit{Hash: "abc", Message: "Add a subnet", AuthorName: "Bob"},
	}
	res, err := r.Evaluate(context.Background(),
		request("${{ run.id }} ${{ run.type }} ${{ run.commit.hash }} ${{ run.commit.message }} "+
			"${{ run.commit.authorName }} ${{ run.triggeredBy }} ${{ run.priority }}"),
		expr.Input{Now: time.Unix(0, 0), Values: map[string]any{surface.Name: surface.Value(run)}},
	)
	want := run.ID.String() + " PROPOSED abc Add a subnet Bob alice low"
	if err != nil || res.Value != want {
		t.Errorf("rendered %v, %v, want %q", res.Value, err, want)
	}
}
