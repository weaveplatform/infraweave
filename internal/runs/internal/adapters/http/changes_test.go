package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/changes"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// planned is one run whose changes document is doc, nil for a run that
// has not planned, on a stack of vendor; open fails with openErr.
type planned struct {
	run     lifecycle.Run
	doc     []byte
	vendor  string
	openErr error
}

func (p *planned) Get(context.Context, kernel.AccountID, kernel.ULID) (lifecycle.Run, error) {
	return p.run, nil
}

func (p *planned) Changes(context.Context, lifecycle.Run) (string, bool, error) {
	return "changes.json", p.doc != nil, nil
}

func (p *planned) Open(context.Context, objectstore.Name, string) (io.ReadCloser, error) {
	if p.openErr != nil {
		return nil, p.openErr
	}
	return io.NopCloser(bytes.NewReader(p.doc)), nil
}

func (p *planned) Settings(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Settings,
	error,
) {
	return stacksapi.Settings{Vendor: p.vendor}, nil
}

// changesOf serves the changes document of internal/iac's recorded
// Terraform plan of one change of every type.
func changesOf(t *testing.T) (*planned, Server, context.Context) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "iac", "plan", "testdata",
		"terraform", "all.json"))
	if err != nil {
		t.Fatal(err)
	}
	pl, err := plan.Parse(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	var doc bytes.Buffer
	if err := plan.Write(&doc, pl.Changes); err != nil {
		t.Fatal(err)
	}
	p := &planned{run: lifecycle.Run{ID: kernel.NewULID()}, doc: doc.Bytes(), vendor: "terraform"}
	s := New(nil, nil, nil, nil, changes.New(changes.Deps{Store: p, Objects: p, Stacks: p}))
	return p, s, kernel.WithPrincipal(context.Background(), kernel.Principal{})
}

// TestUnitRunChangesV2 pages the recorded plan's ten changes four at a
// time from each page's end cursor, each entity split from its address,
// and a filtered list by its own cursors.
func TestUnitRunChangesV2(t *testing.T) {
	p, s, ctx := changesOf(t)
	run, four := p.run.ID.String(), int32(4)
	got := make([]api.EntityChangeConnectionV2, 0, 10)
	var after *string
	for page := range 3 {
		res, err := s.RunChangesV2(ctx, api.RunChangesV2RequestObject{
			RunId: run, Params: api.RunChangesV2Params{
				Input: api.EntityChangeCollectionInputV2{First: &four, After: after},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		body := res.(api.RunChangesV2200JSONResponse).Body
		info := body.PageInfo
		if info.HasNextPage != (page < 2) || info.HasPreviousPage != (page > 0) ||
			info.StartCursor != body.Edges[0].Cursor ||
			info.EndCursor != body.Edges[len(body.Edges)-1].Cursor {
			t.Errorf("page %d: %+v", page, info)
		}
		got = append(got, body.Edges...)
		after = &info.EndCursor
	}
	if len(got) != 10 {
		t.Fatalf("%d changes", len(got))
	}
	for i, want := range map[int]api.EntityChangeV2{
		0: {
			Entity: api.ManagedEntityV2{
				Id: "data.terraform_remote_state.read", Address: "data.terraform_remote_state.read",
				Name: "read", Type: "data.terraform_remote_state",
			},
			Phase: api.EntityChangePhasePLAN, Type: api.EntityChangeTypeREAD,
		},
		1: {
			Entity: api.ManagedEntityV2{
				Id: "terraform_data.add", Address: "terraform_data.add", Name: "add",
				Type: "terraform_data",
			},
			Phase: api.EntityChangePhasePLAN, Type: api.EntityChangeTypeADD,
		},
	} {
		if got[i].Node != want {
			t.Errorf("change %d = %+v, want %+v", i, got[i].Node, want)
		}
	}
	deletes := api.EntityChangeTypeDELETE
	res, err := s.RunChangesV2(ctx, api.RunChangesV2RequestObject{
		RunId: run, Params: api.RunChangesV2Params{
			Input: api.EntityChangeCollectionInputV2{ChangeType: &deletes},
		},
	})
	if body := res.(api.RunChangesV2200JSONResponse).Body; err != nil || len(body.Edges) != 1 ||
		body.Edges[0].Node.Entity.Id != "terraform_data.delete" || body.PageInfo.HasNextPage {
		t.Errorf("deletes = %+v, %v", body, err)
	}
	p.doc = nil
	res, err = s.RunChangesV2(ctx, api.RunChangesV2RequestObject{RunId: run})
	if body := res.(api.RunChangesV2200JSONResponse).Body; err != nil || body.Edges == nil ||
		len(body.Edges) != 0 || body.PageInfo != (api.PageInfo{}) {
		t.Errorf("a run that has not planned = %+v, %v", body, err)
	}
}

// TestUnitRunChangesV2Cursors refuses a cursor that does not decode,
// belongs to another list or run, or names a change the input does not
// select, as an invalid cursor.
func TestUnitRunChangesV2Cursors(t *testing.T) {
	p, s, ctx := changesOf(t)
	run, nope := p.run.ID.String(), "nope"
	cursor := func(list, key, id string) *string {
		c, _ := page.Encode(list, kernel.Cursor[string]{Key: key, ID: id})
		return &c
	}
	adds := api.EntityChangeTypeADD
	for name, in := range map[string]api.EntityChangeCollectionInputV2{
		"a cursor that does not decode": {After: &nope},
		"another list's":                {After: cursor(logsList, "terraform_data.add", run)},
		"another run's": {
			After: cursor(changesList, "terraform_data.add", kernel.NewULID().String()),
		},
		"a change not selected": {
			After: cursor(changesList, "terraform_data.delete", run), ChangeType: &adds,
		},
	} {
		_, err := s.RunChangesV2(ctx, api.RunChangesV2RequestObject{
			RunId: run, Params: api.RunChangesV2Params{Input: in},
		})
		if pr, ok := errors.AsType[*problem.Error](err); !ok ||
			pr.Problem.Type != problem.InvalidCursor.New("").Type {
			t.Errorf("%s = %v", name, err)
		}
	}
}

// TestUnitRunChangesV3 answers the recorded plan's changes as one set, the
// moved one with its previous address, each with no attribute changes;
// none before the run plans; and refuses another vendor's run as invalid
// and an expired document as invalid-state.
func TestUnitRunChangesV3(t *testing.T) {
	p, s, ctx := changesOf(t)
	req := api.RunChangesV3RequestObject{RunId: p.run.ID.String()}
	res, err := s.RunChangesV3(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	body := res.(api.RunChangesV3200JSONResponse).Body
	if len(body) != 1 || len(body[0].Resources) != 10 {
		t.Fatalf("answered %+v", body)
	}
	for _, c := range body[0].Resources {
		moved := c.Address == "terraform_data.new"
		if c.AttributeChanges == nil || len(c.AttributeChanges) != 0 ||
			c.Metadata.Result != api.EntityChangeResultPLANNED || c.Metadata.Moved != moved ||
			moved != (c.PreviousAddress == "terraform_data.old") ||
			!moved && c.PreviousAddress != "" {
			t.Errorf("change %+v", c)
		}
	}
	p.doc = nil
	res, err = s.RunChangesV3(ctx, req)
	if body := res.(api.RunChangesV3200JSONResponse).Body; err != nil || body == nil ||
		len(body) != 0 {
		t.Errorf("a run that has not planned = %+v, %v", body, err)
	}
	p.vendor = "pulumi"
	if _, err := s.RunChangesV3(ctx, req); !errors.As(err, new(*kernel.Invalid)) {
		t.Errorf("another vendor's run = %v", err)
	}
	p.vendor, p.doc, p.openErr = "opentofu", []byte{}, objectstore.ErrNotFound
	_, err = s.RunChangesV3(ctx, req)
	if pr, ok := errors.AsType[*problem.Error](err); !ok ||
		pr.Problem.Type != problem.InvalidState.New("").Type {
		t.Errorf("an expired document = %v", err)
	}
	if _, err := s.RunChangesV2(ctx, api.RunChangesV2RequestObject{RunId: req.RunId}); err == nil {
		t.Error("changes-v2 read an expired document")
	}
}

// TestUnitDeltaOf answers no delta before a plan, and every count the
// plan kept, clamped to int32, with the Ansible counts zero.
func TestUnitDeltaOf(t *testing.T) {
	if d := deltaOf(nil); d != nil {
		t.Errorf("no delta = %+v", d)
	}
	got := deltaOf(&workersapi.Delta{
		Add: 1, Change: 2, Delete: 3, Import: 4, Move: 5, Forget: 6, Resources: math.MaxInt32 + 1,
		Outputs: true,
	})
	if *got != (api.Delta{
		AddCount: 1, ChangeCount: 2, DeleteCount: 3, ImportCount: 4, MoveCount: 5,
		ForgetCount: 6, Resources: math.MaxInt32,
	}) {
		t.Errorf("delta = %+v", got)
	}
	if r := runOf(readView(&workersapi.Delta{Add: 1})); r.Delta == nil || r.Delta.AddCount != 1 {
		t.Errorf("a run's delta = %+v", r.Delta)
	}
	if r := runOf(readView(nil)); r.Delta != nil {
		t.Errorf("a run that has not planned has delta %+v", r.Delta)
	}
}

// readView is a run whose plan's delta is d as the read service views it.
func readView(d *workersapi.Delta) read.View {
	return read.View{Run: lifecycle.Run{ID: kernel.NewULID(), Delta: d}}
}
