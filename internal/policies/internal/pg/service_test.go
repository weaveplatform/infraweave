package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

func TestAccPolicyMetadataSurvivesReplacement(t *testing.T) {
	f := newFixture(t)
	ctx := kernel.WithPrincipal(t.Context(), f.principal)
	origin := kernel.NewULID().String()
	created, err := f.http.PolicyCreatev2(ctx, api.PolicyCreatev2RequestObject{Body: &api.PolicyCreateInput{
		Name: "Metadata", Type: api.PolicyTypePLAN, Body: "package infraweave\n",
		Description: new("original"), Labels: &[]string{"prod", "eu"}, PolicyTemplateULID: &origin,
	}})
	if err != nil {
		t.Fatal(err)
	}
	view, ok := created.(api.PolicyCreatev2201JSONResponse)
	if !ok {
		t.Fatalf("create returned %T", created)
	}
	id, err := kernel.ParseULID(view.Body.Id)
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Get(ctx, f.principal.AccountID, id, false)
	if err != nil || before.Template == nil || *before.Template != origin || before.Description == nil || *before.Description != "original" {
		t.Fatalf("create lost metadata: %+v %v", before, err)
	}
	_, err = f.http.PolicyUpdatev2(
		ctx,
		api.PolicyUpdatev2RequestObject{
			PolicyId: id.String(),
			Body:     &api.PolicyUpdateInput{Name: "Renamed", Body: before.Body, Space: new("team"), Description: new("changed"), Labels: &[]string{"review"}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.store.Get(ctx, f.principal.AccountID, id, false)
	if err != nil || after.Template == nil || *after.Template != origin || after.Type != before.Type || !after.CreatedAt.Equal(before.CreatedAt) || after.Name != "Renamed" ||
		after.Description == nil ||
		*after.Description != "changed" ||
		len(after.Labels) != 1 ||
		after.Labels[0] != "review" {
		t.Fatalf("replacement changed immutable metadata or lost mutable settings: %+v %v", after, err)
	}
}

// The attachment is an authored relation fixture: attachment APIs belong to
// POL-06. This verifies POL-05's C9 delete boundary against actual PostgreSQL.
func TestAccAttachedPolicyDeletion(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Guard")
	acct := f.principal.AccountID
	if _, err := f.db.Exec(
		t.Context(),
		`INSERT INTO policy_stack_attachments (account_id,id,policy_id,stack_id,created_at) VALUES ($1,$5,$2,$3,$4)`,
		acct,
		p.ID,
		kernel.NewULID(),
		f.now,
		kernel.NewULID(),
	); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.Delete(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, false)
	var invalid *kernel.Invalid
	if !errors.As(err, &invalid) || f.count(t, "policies") != 1 || f.count(t, "policy_stack_attachments") != 1 || f.count(t, "events") != 1 {
		t.Fatalf("unforced deletion changed policy or attachment: %v", err)
	}
	out, err := f.svc.Delete(t.Context(), f.principal, p.ID.String(), kernel.ExpectVersions(p.Version), true)
	if err != nil || out.ID != p.ID || !out.CreatedAt.Equal(p.CreatedAt) || !out.UpdatedAt.Equal(p.UpdatedAt) {
		t.Fatalf("forced delete view: %+v, %v", out, err)
	}
	if f.count(t, "policies") != 0 || f.count(t, "policy_stack_attachments") != 0 || f.count(t, "events") != 2 {
		t.Fatal("force deletion did not commit policy, attachment and event together")
	}
	var sequence int64
	var document []byte
	if err := f.db.QueryRow(t.Context(), `SELECT sequence,document FROM events WHERE account_id=$1 AND subject_id=$2 ORDER BY sequence DESC LIMIT 1`, acct, p.ID.String()).
		Scan(&sequence, &document); err != nil {
		t.Fatal(err)
	}
	if sequence != 2 || strings.Contains(string(document), p.Body) {
		t.Fatalf("delete event leaks source or lacks next sequence: %d %s", sequence, document)
	}
	var decoded map[string]any
	if err := json.Unmarshal(document, &decoded); err != nil || decoded["change"] != "deleted" || decoded["policy"] != p.ID.String() {
		t.Fatalf("delete event: %s, %v", document, err)
	}
}

type failedEvents struct{ err error }

func (f failedEvents) Append(context.Context, outbox.Event) (outbox.Event, error) {
	return outbox.Event{}, f.err
}

func TestAccPolicyWritesRollbackWhenEventFails(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Stable")
	injected := errors.New("injected event append failure")
	svc := f.service(failedEvents{err: injected})
	for _, action := range []string{"create", "update", "delete"} {
		t.Run(action, func(t *testing.T) {
			var err error
			input := core.Input{Name: "Must roll back", Body: p.Body, Type: p.Type}
			switch action {
			case "create":
				_, err = svc.Create(t.Context(), f.principal, input)
			case "update":
				_, err = svc.Update(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, input)
			case "delete":
				_, err = svc.Delete(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, true)
			}
			if !errors.Is(err, injected) {
				t.Fatalf("lost append failure: %v", err)
			}
			stored, err := f.store.Get(t.Context(), p.Account, p.ID, false)
			if err != nil || stored.Name != p.Name || stored.Version != p.Version || f.count(t, "policies") != 1 || f.count(t, "events") != 1 {
				t.Fatalf("failed %s committed state: %+v %v", action, stored, err)
			}
		})
	}
}

func TestAccPolicyTimestampsAndPurgeResume(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Original")
	f.now = f.now.Add(3 * time.Second)
	updated, err := f.svc.Update(t.Context(), f.principal, p.ID.String(), kernel.ExpectVersions(1), core.Input{Name: "Updated", Body: p.Body})
	if err != nil || !updated.CreatedAt.Equal(p.CreatedAt) || !updated.UpdatedAt.Equal(f.now) || updated.Version != 2 {
		t.Fatalf("update timestamps: %+v %v", updated, err)
	}
	f.create(t, "Another")
	other := p
	other.Account = kernel.NewAccountID()
	other.ID = kernel.NewULID()
	if err := f.store.Create(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		done bool
		left int
	}{{false, 1}, {false, 0}, {true, 0}, {true, 0}} {
		done, err := f.store.Purge(t.Context(), p.Account, 1)
		if err != nil || done != want.done || f.count(t, "policies") != want.left {
			t.Fatalf("purge resume: done %v %v, want %+v", done, err, want)
		}
	}
	if _, err := f.store.Get(t.Context(), other.Account, other.ID, false); err != nil {
		t.Fatalf("purge crossed accounts: %v", err)
	}
}
