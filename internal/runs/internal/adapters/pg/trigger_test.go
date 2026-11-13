package pg_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/trigger"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// triggerer is a trigger service of w's, with w's sources bound when
// bound is set.
func (w *world) triggerer(bound bool) *trigger.Service {
	s := trigger.New(trigger.Deps{
		DB: w.db, Stacks: w.stacks, Lifecycle: w.lc, Views: w.reader(w.access()),
	})
	if bound {
		s.BindSources(w.sources)
	}
	return s
}

// alice is a member of w's account.
func (w *world) alice() kernel.Principal {
	return kernel.Principal{
		Kind: kernel.PrincipalUser, ActorID: kernel.NewULID(), AccountID: w.acct, Login: "alice",
	}
}

// TestAccTrigger triggers runs on the stack's tracked commit, else its
// branch head, and on a named commit; answers a second trigger of one
// stack, type and commit with the first run; stores the runtime
// configuration and force-apply mode; and refuses another type, an
// unknown commit, a stack with no commit, an unknown stack, a named
// commit while no source is bound and a disabled stack, even for a run it
// holds.
func TestAccTrigger(t *testing.T) {
	w := newWorld(t)
	s := w.triggerer(true)
	head := stacksapi.Commit{Hash: "head", Message: "Head", Timestamp: w.now.Add(-time.Hour)}
	tracked := stacksapi.Commit{Hash: "tracked", Message: "Pinned", AuthorName: "Bob"}
	w.sources.commits = map[string]stacksapi.Commit{"named": {Hash: "named", Message: "Named"}}
	st := w.stacks.settings[w.stack]
	st.Branch = "main"
	w.stacks.settings[w.stack] = st

	if _, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{}); !errors.Is(err,
		trigger.ErrNoCommit) {
		t.Errorf("a stack with no commit = %v, want ErrNoCommit", err)
	}
	st.TrackedBranchHead = &head
	w.stacks.settings[w.stack] = st
	v, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{})
	if err != nil || v.Run.Commit.Hash != "head" || v.Run.Type != kernel.RunTypeTracked ||
		v.Run.Commit.Branch != "main" || v.Stack.Slug != "network" || len(v.History) != 1 ||
		v.History[0].State != kernel.RunStateQueued || *v.History[0].Username != "alice" {
		t.Fatalf("on the branch head: %+v, %v", v, err)
	}
	st.TrackedCommit = &tracked
	w.stacks.settings[w.stack] = st
	rc := json.RawMessage(`{"runnerImage":"custom"}`)
	v, err = s.Trigger(t.Context(), w.alice(), "network", trigger.Input{
		Type: kernel.RunTypeProposed, RuntimeConfig: rc, ForceApply: "SINGLE",
	})
	if err != nil || v.Run.Commit.Hash != "tracked" || v.Run.Type != kernel.RunTypeProposed {
		t.Fatalf("on the tracked commit: %+v, %v", v.Run, err)
	}
	var (
		config []byte
		force  string
	)
	if err := w.db.QueryRow(t.Context(), `SELECT runtime_config, force_apply_mode FROM runs
		WHERE account_id = $1 AND id = $2`, w.acct, v.Run.ID).Scan(&config, &force); err != nil ||
		string(config) != `{"runnerImage": "custom"}` || force != "SINGLE" {
		t.Errorf("stored runtime config %s and force-apply %q, %v", config, force, err)
	}
	again, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{
		Type: kernel.RunTypeProposed,
	})
	if err != nil || again.Run.ID != v.Run.ID || string(again.Run.RuntimeConfig) == "" ||
		again.Run.Commit.Branch != "main" {
		t.Errorf("a second trigger = %+v, %v; want %s with its configuration and branch",
			again.Run, err, v.Run.ID)
	}
	named, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{CommitSHA: "named"})
	if err != nil || named.Run.Commit.Hash != "named" || named.Run.Commit.Message != "Named" ||
		named.Run.Commit.Branch != "main" {
		t.Errorf("on a named commit: %+v, %v", named.Run.Commit, err)
	}

	ovs := []lifecycle.Override{{Name: "TOKEN", Value: "override-s3cret"}, {Name: "DEBUG"}}
	over, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{
		Type: kernel.RunTypeProposed, Overrides: ovs,
	})
	if err != nil || over.Run.ID == v.Run.ID || over.Run.Commit.Hash != "tracked" {
		t.Fatalf("a proposal with overrides = %+v, %v; want a run of its own", over.Run, err)
	}
	var sealed []byte
	if err := w.db.QueryRow(t.Context(), `SELECT env_overrides_sealed FROM runs
		WHERE account_id = $1 AND id = $2`, w.acct, over.Run.ID).Scan(&sealed); err != nil ||
		sealed == nil || bytes.Contains(sealed, []byte("override-s3cret")) {
		t.Errorf("the stored overrides %q, %v; want them sealed", sealed, err)
	}
	if got, err := w.store.Overrides(t.Context(), over.Run); err != nil || !slices.Equal(got, ovs) {
		t.Errorf("the overrides = %+v, %v", got, err)
	}
	if got, err := w.store.Overrides(t.Context(), v.Run); err != nil || got != nil {
		t.Errorf("the overrides of a run without = %+v, %v", got, err)
	}
	twice, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{
		Type: kernel.RunTypeProposed, Overrides: ovs,
	})
	if err != nil || twice.Run.ID == over.Run.ID {
		t.Errorf("the same overrides again = %s, %v; want another run", twice.Run.ID, err)
	}
	if plain, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{
		Type: kernel.RunTypeProposed,
	}); err != nil || plain.Run.ID != v.Run.ID {
		t.Errorf("a proposal without overrides = %s, %v; want %s", plain.Run.ID, err, v.Run.ID)
	}

	for name, c := range map[string]struct {
		s       *trigger.Service
		slug    string
		in      trigger.Input
		pointer string
		is      error
	}{
		"a task": {s, "network", trigger.Input{Type: kernel.RunTypeTask}, "/runType", nil},
		"overrides on a tracked run": {
			s, "network",
			trigger.Input{Overrides: []lifecycle.Override{{Name: "A", Value: "1"}}},
			"/environmentVarsOverrides", nil,
		},
		"an infraweave variable": {
			s, "network",
			trigger.Input{Type: kernel.RunTypeProposed, Overrides: []lifecycle.Override{
				{Name: "TF_VAR_infraweave_run_id", Value: "1"},
			}},
			"/environmentVarsOverrides/0/key", nil,
		},
		"an unknown SHA":  {s, "network", trigger.Input{CommitSHA: "gone"}, "/commitSha", nil},
		"an unknown slug": {s, "gone", trigger.Input{}, "", stacksapi.ErrNotFound},
		"no sources": {
			w.triggerer(false), "network",
			trigger.Input{CommitSHA: "named"},
			"",
			kernel.ErrUnavailable,
		},
	} {
		_, err := c.s.Trigger(t.Context(), w.alice(), c.slug, c.in)
		if c.is != nil {
			if !errors.Is(err, c.is) {
				t.Errorf("%s = %v, want %v", name, err, c.is)
			}
			continue
		}
		inv, ok := errors.AsType[*kernel.Invalid](err)
		if !ok || len(inv.Errors) != 1 || inv.Errors[0].Pointer != c.pointer {
			t.Errorf("%s = %v, want Invalid at %s", name, err, c.pointer)
		}
	}

	w.stacks.err["Settings"] = errInjected
	if _, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{}); !errors.Is(err,
		errInjected) {
		t.Errorf("a failed settings read = %v", err)
	}
	delete(w.stacks.err, "Settings")
	st.Disabled = true
	w.stacks.settings[w.stack] = st
	if _, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{
		CommitSHA: "named",
	}); !errors.Is(err, lifecycle.ErrDisabled) {
		t.Errorf("a disabled stack = %v, want ErrDisabled", err)
	}
}

// refused reports whether err is a *kernel.Invalid refusing one field,
// at pointer.
func refused(err error, pointer string) bool {
	inv, ok := errors.AsType[*kernel.Invalid](err)
	return ok && len(inv.Errors) == 1 && inv.Errors[0].Pointer == pointer
}

// uploads is the uploads store as the trigger sees it: each key's
// declared size while pending, the size its PUT stored, and whether it is
// live, with errors injected by method.
type uploads struct {
	declared map[string]int64
	stored   map[string]int64
	live     map[string]bool
	err      map[string]error
}

func newUploads() *uploads {
	return &uploads{
		declared: map[string]int64{}, stored: map[string]int64{}, live: map[string]bool{},
		err: map[string]error{},
	}
}

func (u *uploads) Begin(
	_ context.Context,
	_ objectstore.Name,
	key string,
	size int64,
	_ []byte,
) error {
	u.declared[key] = size
	return u.err["Begin"]
}

func (u *uploads) SignPut(
	_ context.Context,
	_ objectstore.Name,
	key string,
	size int64,
	_ []byte,
) (objectstore.Signed, error) {
	return objectstore.Signed{
		URL:    "https://store.test/uploads/" + key,
		Header: http.Header{"Content-Length": {strconv.FormatInt(size, 10)}},
	}, u.err["SignPut"]
}

func (u *uploads) CommitUpload(
	_ context.Context,
	_ objectstore.Name,
	key string,
) (objectstore.Object, error) {
	size, pending := u.declared[key]
	stored, put := u.stored[key]
	switch {
	case !pending || !put:
		return objectstore.Object{}, blobs.ErrNotUploaded
	case stored != size:
		return objectstore.Object{}, objectstore.ErrSize
	}
	u.live[key] = true
	return objectstore.Object{Size: size}, u.err["CommitUpload"]
}

func (u *uploads) Live(_ context.Context, _ objectstore.Name, key string) (bool, error) {
	return u.live[key], u.err["Live"]
}

// TestAccTriggerLocalPreview uploads a local workspace and proposes a run
// on it, with the stack's tracked commit and the upload recorded, and
// answers that run again for the same upload; and refuses a stack that
// does not enable local previews, a declaration no archive makes, an
// upload that is not the stack's, was never put or holds other bytes, a
// stack with no commit, and each failed call.
func TestAccTriggerLocalPreview(t *testing.T) {
	w := newWorld(t)
	u := newUploads()
	s := trigger.New(trigger.Deps{
		DB: w.db, Stacks: w.stacks, Lifecycle: w.lc, Views: w.reader(w.access()), Objects: u,
	})
	ctx, alice := t.Context(), w.alice()
	sum := make([]byte, sha256.Size)
	st := w.stacks.settings[w.stack]
	st.Branch = "main"
	w.stacks.settings[w.stack] = st

	if _, err := s.UploadWorkspace(ctx, alice, "network", 10, sum); !errors.Is(err,
		trigger.ErrLocalPreview) {
		t.Errorf("an upload to a stack without local previews = %v", err)
	}
	if _, err := s.ProposeWorkspace(
		ctx,
		alice,
		"network",
		kernel.NewULID().String(),
		nil,
	); !errors.Is(
		err,
		trigger.ErrLocalPreview,
	) {
		t.Errorf("a proposal on a stack without local previews = %v", err)
	}
	st.LocalPreviewEnabled = true
	w.stacks.settings[w.stack] = st

	for _, c := range []struct {
		name     string
		size     int64
		sum      []byte
		pointers []string
	}{
		{"an empty archive", 0, sum, []string{"/size"}},
		{"an archive over the bound", trigger.MaxWorkspace + 1, sum, []string{"/size"}},
		{"a short SHA-256", 10, sum[:31], []string{"/sha256"}},
		{"neither", -1, nil, []string{"/size", "/sha256"}},
	} {
		_, err := s.UploadWorkspace(ctx, alice, "network", c.size, c.sum)
		inv, ok := errors.AsType[*kernel.Invalid](err)
		if !ok || len(inv.Errors) != len(c.pointers) {
			t.Errorf("%s = %v, want Invalid at %v", c.name, err, c.pointers)
			continue
		}
		for i, p := range c.pointers {
			if inv.Errors[i].Pointer != p {
				t.Errorf("%s = %v, want Invalid at %v", c.name, err, c.pointers)
			}
		}
	}

	up, err := s.UploadWorkspace(ctx, alice, "network", trigger.MaxWorkspace, sum)
	key := fmt.Sprintf("%s/%s/%s", w.acct, w.stack, up.ID)
	if err != nil || up.URL != "https://store.test/uploads/"+key ||
		up.Header["Content-Length"] != strconv.FormatInt(trigger.MaxWorkspace, 10) {
		t.Fatalf("an upload = %+v, %v", up, err)
	}
	other := kernel.NewULID()
	u.declared[fmt.Sprintf("%s/%s/%s", w.acct, kernel.NewULID(), other)] = 10
	for name, id := range map[string]string{
		"an upload never put": up.ID.String(), "another stack's upload": other.String(),
		"an unknown upload": kernel.NewULID().String(), "no ULID": "workspace",
	} {
		_, err := s.ProposeWorkspace(ctx, alice, "network", id, nil)
		if inv, ok := errors.AsType[*kernel.Invalid](err); !ok ||
			inv.Errors[0].Pointer != "/workspace" || inv.Errors[0].Reason != kernel.ReasonNotFound {
			t.Errorf("%s = %v, want Invalid at /workspace", name, err)
		}
	}
	u.stored[key] = 10
	if _, err := s.ProposeWorkspace(ctx, alice, "network", up.ID.String(), nil); err == nil ||
		!strings.Contains(err.Error(), "holds other bytes") {
		t.Errorf("an upload of other bytes = %v", err)
	}
	u.stored[key] = trigger.MaxWorkspace

	if _, err := s.ProposeWorkspace(ctx, alice, "network", up.ID.String(), nil); !errors.Is(err,
		trigger.ErrNoCommit) {
		t.Errorf("a stack with no commit = %v, want ErrNoCommit", err)
	}
	tracked := stacksapi.Commit{Hash: "tracked", Message: "Pinned"}
	st.TrackedCommit = &tracked
	w.stacks.settings[w.stack] = st
	v, err := s.ProposeWorkspace(ctx, alice, "network", up.ID.String(), nil)
	if err != nil || v.Run.Type != kernel.RunTypeProposed || v.Run.LocalWorkspace != key ||
		!v.Run.LocalPreview() || v.Run.Commit.Hash != "tracked" || v.Run.Commit.Branch != "main" ||
		!u.live[key] {
		t.Fatalf("a proposal = %+v, %v", v.Run, err)
	}
	again, err := s.ProposeWorkspace(ctx, alice, "network", up.ID.String(), nil)
	if err != nil || again.Run.ID != v.Run.ID {
		t.Errorf("a second proposal = %s, %v; want %s", again.Run.ID, err, v.Run.ID)
	}
	commit, err := s.Trigger(ctx, alice, "network", trigger.Input{Type: kernel.RunTypeProposed})
	if err != nil || commit.Run.ID == v.Run.ID || commit.Run.LocalPreview() {
		t.Errorf("a proposal on the tracked commit = %+v, %v", commit.Run, err)
	}
	ovs := []lifecycle.Override{{Name: "DEBUG", Value: "1"}}
	over, err := s.ProposeWorkspace(ctx, alice, "network", up.ID.String(), ovs)
	if err != nil || over.Run.ID == v.Run.ID || over.Run.LocalWorkspace != key {
		t.Errorf("a proposal with overrides on the same upload = %+v, %v", over.Run, err)
	}
	if again, err := s.ProposeWorkspace(ctx, alice, "network", up.ID.String(), nil); err != nil ||
		again.Run.ID != v.Run.ID {
		t.Errorf("a proposal after one with overrides = %s, %v; want %s", again.Run.ID, err,
			v.Run.ID)
	}
	bad := []lifecycle.Override{{Name: "INFRAWEAVE_API_TOKEN", Value: "x"}}
	if _, err := s.ProposeWorkspace(ctx, alice, "network", up.ID.String(), bad); !refused(err,
		"/environmentVarsOverrides/0/key") {
		t.Errorf("a proposal overriding infraweave's variable = %v", err)
	}

	if _, err := s.UploadWorkspace(ctx, alice, "gone", 10, sum); !errors.Is(err,
		stacksapi.ErrNotFound) {
		t.Errorf("an unknown stack = %v", err)
	}
	w.stacks.err["Settings"] = errInjected
	if _, err := s.ProposeWorkspace(ctx, alice, "network", up.ID.String(), nil); !errors.Is(err,
		errInjected) {
		t.Errorf("a failed settings read = %v", err)
	}
	delete(w.stacks.err, "Settings")
	for _, method := range []string{"Begin", "SignPut"} {
		u.err[method] = errInjected
		if _, err := s.UploadWorkspace(ctx, alice, "network", 10, sum); !errors.Is(err,
			errInjected) {
			t.Errorf("a failed %s = %v", method, err)
		}
		delete(u.err, method)
	}
	u.err["Live"] = errInjected
	if _, err := s.ProposeWorkspace(ctx, alice, "network", up.ID.String(), nil); !errors.Is(err,
		errInjected) {
		t.Errorf("a failed Live = %v", err)
	}
	delete(u.err, "Live")
	st.Disabled = true
	w.stacks.settings[w.stack] = st
	up, err = s.UploadWorkspace(ctx, alice, "network", 10, sum)
	if err != nil {
		t.Fatal(err)
	}
	u.stored[fmt.Sprintf("%s/%s/%s", w.acct, w.stack, up.ID)] = 10
	if _, err := s.ProposeWorkspace(ctx, alice, "network", up.ID.String(), nil); !errors.Is(err,
		lifecycle.ErrDisabled) {
		t.Errorf("a disabled stack = %v, want ErrDisabled", err)
	}
}
