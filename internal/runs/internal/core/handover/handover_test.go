package handover_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/handover"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/surface"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vendorsapi "github.com/weaveplatform/infraweave/internal/vendors/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

var errDown = errors.New("down")

// store holds one run and its artifact key; err fails Handed and keyErr
// ArtifactKey.
type store struct {
	run                      lifecycle.Run
	key                      []byte
	overrides                []lifecycle.Override
	err, keyErr, overrideErr error
}

func (s *store) Overrides(context.Context, lifecycle.Run) ([]lifecycle.Override, error) {
	return s.overrides, s.overrideErr
}

func (s *store) Handed(_ context.Context, id kernel.ULID) (lifecycle.Run, error) {
	if s.err != nil {
		return lifecycle.Run{}, s.err
	}
	if id != s.run.ID {
		return lifecycle.Run{}, lifecycle.ErrNotFound
	}
	return s.run, nil
}

func (s *store) ArtifactKey(context.Context, lifecycle.Run) (crypt.Value, error) {
	return crypt.NewValue(s.key), s.keyErr
}

// stacks answers one stack's settings and labels; fail names the method
// that fails.
type stacks struct {
	stacksapi.Stacks
	settings stacksapi.Settings
	labels   []string
	fail     string
}

func (s *stacks) Settings(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
) (stacksapi.Settings, error) {
	if s.fail == "Settings" {
		return stacksapi.Settings{}, errDown
	}
	return s.settings, nil
}

func (s *stacks) Labels(
	_ context.Context,
	_ kernel.AccountID,
	ids []kernel.ULID,
) (map[kernel.ULID][]string, error) {
	if s.fail == "Labels" {
		return nil, errDown
	}
	return map[kernel.ULID][]string{ids[0]: s.labels}, nil
}

// spaces answers slugs, or err.
type spaces struct {
	accessapi.Spaces
	slugs map[kernel.ULID]string
	err   error
}

func (s *spaces) Slugs(context.Context, kernel.AccountID) (map[kernel.ULID]string, error) {
	return s.slugs, s.err
}

// accounts names every account slug, or fails with err.
type accounts struct {
	slug string
	err  error
}

func (a *accounts) Slug(context.Context, kernel.AccountID) (string, error) {
	return a.slug, a.err
}

// environment resolves every stack to resolved, or fails with err.
type environment struct {
	resolved configapi.Resolved
	err      error
}

func (e *environment) Resolve(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
) (configapi.Resolved, error) {
	return e.resolved, e.err
}

// tokens mints in-run tokens, recording each request, or fails with err.
type tokens struct {
	got []accessapi.InRunRequest
	err error
}

func (t *tokens) InRunToken(_ context.Context, r accessapi.InRunRequest) (accessapi.Token, error) {
	t.got = append(t.got, r)
	return accessapi.Token{Raw: "in-run", Expires: time.Unix(2e9, 0)}, t.err
}

// catalog lists versions, newest first, for every vendor, and answers
// Effective with effective, recording each constraint it is asked;
// err fails both.
type catalog struct {
	versions  []string
	effective string
	asked     []string
	invalid   bool
	err       error
}

func (c *catalog) Versions(context.Context, vendorsapi.Vendor) ([]string, error) {
	return c.versions, c.err
}

func (c *catalog) Effective(
	_ context.Context,
	_ vendorsapi.Vendor,
	constraints string,
) (string, error) {
	c.asked = append(c.asked, constraints)
	if c.invalid {
		return "", &kernel.Invalid{Detail: "none", Errors: []kernel.FieldError{{
			Pointer: "#/query/constraints", Reason: kernel.ReasonRule,
		}}}
	}
	return c.effective, c.err
}

// sources answers every run's source with src, recording each commit it
// is asked for, or fails with err.
type sources struct {
	src   runsapi.Source
	asked []string
	err   error
}

func (s *sources) Source(
	_ context.Context,
	_ kernel.AccountID,
	_, _ kernel.ULID,
	commit string,
) (runsapi.Source, error) {
	s.asked = append(s.asked, commit)
	return s.src, s.err
}

func (*sources) Release(context.Context, kernel.AccountID, kernel.ULID) error { return nil }

// objects signs a GET of each live key of the uploads store, answers
// objectstore.ErrNotFound for any other, or fails with err.
type objects struct {
	live map[string]bool
	err  error
}

func (o *objects) SignGet(
	_ context.Context,
	store objectstore.Name,
	key string,
) (objectstore.Signed, error) {
	switch {
	case o.err != nil:
		return objectstore.Signed{}, o.err
	case store != objectstore.Uploads || !o.live[key]:
		return objectstore.Signed{}, objectstore.ErrNotFound
	}
	return objectstore.Signed{URL: "https://store.test/uploads/" + key}, nil
}

// secret is an opened value.
type secret string

func (s secret) Reveal() []byte { return []byte(s) }

type (
	db     struct{}
	fakeTx struct{ pgx.Tx }
)

func (db) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return fakeTx{}, nil }
func (fakeTx) Commit(context.Context) error                       { return nil }
func (fakeTx) Rollback(context.Context) error                     { return nil }

// registry is the environment surface as the configuration and runs
// domains declare it.
func registry(t *testing.T) *expr.Registry {
	t.Helper()
	r, err := expr.NewRegistry(expr.Defaults(),
		expr.Surface{Name: configapi.Surface, Variables: []expr.Variable{
			{
				Name: configapi.EnvVariable,
				Schema: json.RawMessage(
					`{"type": "object", "additionalProperties": {"type": "string"}}`,
				),
			},
			{
				Name: configapi.StackVariable,
				Schema: json.RawMessage(`{"type": "object", "properties": {
				"id": {"type": "string"}, "space": {"type": "string"},
				"labels": {"type": "array", "items": {"type": "string"}},
				"projectRoot": {"type": "string"}
			}, "additionalProperties": true}`),
			},
		}},
		expr.Surface{Name: configapi.Surface, Variables: []expr.Variable{surface.Variable()}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// world is a service over one run of stack infra, on a worker of a pool,
// whose environment the test sets.
type world struct {
	svc      *handover.Service
	store    *store
	stacks   *stacks
	spaces   *spaces
	accounts *accounts
	env      *environment
	tokens   *tokens
	catalog  *catalog
	sources  *sources
	objects  *objects
	run      lifecycle.Run
	space    kernel.ULID
}

func newWorld(t *testing.T) *world {
	t.Helper()
	pool, space := kernel.NewULID(), kernel.NewULID()
	stackRoot, image := "stack-root", "ghcr.io/acme/stack"
	run := lifecycle.Run{
		Account: kernel.NewAccountID(), ID: kernel.NewULID(), Stack: kernel.NewULID(),
		Type: kernel.RunTypeTracked, State: kernel.RunStatePreparing, Version: 2,
		StateChangedAt: time.Unix(1_800_000_000, 0).UTC(),
		Commit: stacksapi.Commit{
			Hash: "abc", Message: "Fix", AuthorName: "Alice", Branch: "main",
		},
		TriggeredBy: "alice", Priority: kernel.RunPriorityNormal, WorkerPool: &pool,
		CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	w := &world{
		store: &store{run: run, key: []byte("0123456789abcdef0123456789abcdef")},
		stacks: &stacks{
			settings: stacksapi.Settings{
				Stack: stacksapi.Stack{
					ID:    run.Stack,
					Slug:  "infra",
					Name:  "Infra",
					Space: space,
				},
				Repository:   "infra",
				Branch:       "main",
				ProjectRoot:  &stackRoot,
				RunnerImage:  &image,
				Vendor:       "terraform",
				VendorConfig: json.RawMessage(`{"version":"1.9"}`),
			},
			labels: []string{"a", "b"},
		},
		spaces:   &spaces{slugs: map[kernel.ULID]string{space: "legacy"}},
		accounts: &accounts{slug: "acme"},
		env:      &environment{},
		tokens:   &tokens{},
		catalog:  &catalog{versions: []string{"1.14.2", "1.13.0"}, effective: "1.9.8"},
		sources: &sources{src: runsapi.Source{
			URL: "https://git.example.com/acme/infra.git",
			Credential: &runsapi.SourceCredential{
				Username: "x-access-token", Token: []byte("narrow"), Narrowed: true,
			},
		}},
		objects: &objects{live: map[string]bool{"acct/stack/upload": true}},
		run:     run,
		space:   space,
	}
	w.svc = handover.New(handover.Deps{
		Store: w.store, Stacks: w.stacks, Spaces: w.spaces, Accounts: w.accounts,
		Environment: w.env, Tokens: w.tokens, Catalog: w.catalog, Objects: w.objects,
	})
	w.svc.BindExpressions(registry(t))
	w.svc.BindSources(w.sources)
	return w
}

// handover hands over id in a transaction.
func (w *world) handover(t *testing.T, id kernel.ULID) (workersapi.Handover, error) {
	t.Helper()
	var h workersapi.Handover
	err := tx.Do(t.Context(), db{}, tx.Options{}, func(ctx context.Context) error {
		var err error
		h, err = w.svc.Handover(ctx, id)
		return err
	})
	return h, err
}

// variable is v of the environment.
func variable(name string, value string, writeOnly bool) configapi.Element {
	return configapi.Element{Name: name, Value: secret(value), WriteOnly: writeOnly}
}

// sealed is the secrets document of h.
type sealed struct {
	Environment       map[string]string `json:"environment"`
	Files             map[string][]byte `json:"files"`
	APIToken          string            `json:"apiToken"`
	APITokenExpiresAt int64             `json:"apiTokenExpiresAt"`
	ArtifactKey       []byte            `json:"artifactKey"`
	SourceCredential  *struct {
		Username string `json:"username"`
		Token    string `json:"token"`
	} `json:"sourceCredential"`
}

// TestUnitHandover checks that a handover renders each variable's
// templates against env, stack and run, puts a write-only value and one
// whose template reads a write-only variable in the secrets, by name
// alone in the spec, with the in-run API token and the artifact key, and
// adds infraweave's own variables unless the stack sets one; that the
// runtime configuration's runner image and project root win over the
// stack's; and that a stack with no labels has an empty list.
func TestUnitHandover(t *testing.T) {
	w := newWorld(t)
	w.store.run.RuntimeConfig = json.RawMessage(
		`{"runnerImage":"ghcr.io/acme/run","projectRoot":"run-root"}`)
	mode := "600"
	w.env.resolved = configapi.Resolved{
		Elements: []configapi.Element{
			variable("REGION", "eu", false),
			variable("TOKEN", "s3cret", true),
			variable("URL", "https://${{ env.REGION }}/${{ stack.id }}/${{ run.type }}", false),
			variable("AUTH", "Bearer ${{ env.TOKEN }}", false),
			variable("SHELL", "${HOME}/bin", false),
			variable("COUNT", "${{ 1 + 2 }}", false),
			variable("ROOT", "${{ stack.projectRoot }}:${{ stack.labels[1] }}:${{ stack.space }}",
				false),
			variable("TF_VAR_infraweave_stack_id", "mine", false),
			{Name: "EMPTY"},
			{Name: "main.tf", FileMount: true, Checksum: "x", Value: secret("terraform {}")},
			{
				Name: "key.pem", FileMount: true, WriteOnly: true, FileMode: &mode, Checksum: "y",
				Value: secret("PRIVATE"),
			},
		},
		Hooks: map[string][]string{"before_init": {"echo hi"}},
	}
	h, err := w.handover(t, w.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if h.Account != w.run.Account || h.Pool != *w.run.WorkerPool {
		t.Errorf("handover of %s in %s", h.Account, h.Pool)
	}
	spec := h.Spec
	if spec.Run != (workersapi.SpecRun{
		Account: w.run.Account, ID: w.run.ID, Type: kernel.RunTypeTracked, State: kernel.RunStatePreparing,
		StateVersion: 2, TriggeredBy: "alice", Priority: kernel.RunPriorityNormal,
		CreatedAt: w.run.CreatedAt,
	}) {
		t.Errorf("run = %+v", spec.Run)
	}
	if st := spec.Stack; st.Slug != "infra" || st.Space != "legacy" ||
		!slices.Equal(st.Labels, []string{"a", "b"}) || *st.ProjectRoot != "run-root" ||
		st.Vendor != "terraform" || string(st.VendorConfig) != `{"version":"1.9"}` ||
		st.EnableWellKnownSecretMasking {
		t.Errorf("stack = %+v", st)
	}
	if *spec.RunnerImage != "ghcr.io/acme/run" || spec.Commit.Hash != "abc" ||
		!slices.Equal(spec.Hooks["before_init"], []string{"echo hi"}) {
		t.Errorf("image %s, commit %+v, hooks %v", *spec.RunnerImage, spec.Commit, spec.Hooks)
	}
	plain := map[string]string{}
	var hidden, names []string
	for _, v := range spec.Environment {
		names = append(names, v.Name)
		if v.WriteOnly {
			if v.Value != nil {
				t.Errorf("%s is write-only with a value", v.Name)
			}
			hidden = append(hidden, v.Name)
			continue
		}
		plain[v.Name] = *v.Value
	}
	if !slices.IsSorted(names) || !slices.Equal(hidden, []string{"AUTH", "TOKEN"}) {
		t.Errorf("variables %v, write-only %v", names, hidden)
	}
	for name, want := range map[string]string{
		"REGION": "eu", "URL": "https://eu/infra/TRACKED", "SHELL": "${HOME}/bin", "COUNT": "3",
		"ROOT": "run-root:b:legacy", "EMPTY": "", "TF_VAR_infraweave_stack_id": "mine",
		"TF_VAR_infraweave_run_id": w.run.ID.String(), "TF_VAR_infraweave_commit_sha": "abc",
		"TF_VAR_infraweave_project_root": "run-root", "TF_VAR_infraweave_repository": "infra",
		"TF_VAR_infraweave_run_trigger": "alice", "TF_VAR_infraweave_run_type": "TRACKED",
		"TF_VAR_infraweave_space_id": "legacy", "TF_VAR_infraweave_stack_branch": "main",
		"TF_VAR_infraweave_stack_labels": "a,b", "TF_VAR_infraweave_stack_labels_list": `["a","b"]`,
		"TF_VAR_infraweave_workspace_root": handover.WorkspaceRoot,
		"TF_VAR_infraweave_account_name":   "acme", "TF_VAR_infraweave_commit_branch": "main",
		"TF_VAR_infraweave_local_preview": "false",
	} {
		if plain[name] != want {
			t.Errorf("%s = %q, want %q", name, plain[name], want)
		}
	}
	if len(spec.Files) != 2 || spec.Files[0].Path != "key.pem" || spec.Files[0].Content != nil ||
		!spec.Files[0].WriteOnly || *spec.Files[0].Mode != "600" ||
		string(spec.Files[1].Content) != "terraform {}" {
		t.Errorf("files = %+v", spec.Files)
	}
	if fmt.Sprint(h.Secrets) != "[redacted]" {
		t.Errorf("the secrets print as %v", h.Secrets)
	}
	var got sealed
	if err := json.Unmarshal(h.Secrets.Reveal(), &got); err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{"TOKEN": "s3cret", "AUTH": "Bearer s3cret"}
	if !maps.Equal(got.Environment, secrets) || string(got.Files["key.pem"]) != "PRIVATE" ||
		len(got.Files) != 1 || got.APIToken != "in-run" || got.APITokenExpiresAt != 2e9 ||
		string(got.ArtifactKey) != string(w.store.key) || got.SourceCredential == nil ||
		got.SourceCredential.Username != "x-access-token" ||
		got.SourceCredential.Token != "narrow" {
		t.Errorf("secrets = %+v", got)
	}
	if spec.Source != (workersapi.SpecSource{
		Kind: workersapi.SourceGit, URL: "https://git.example.com/acme/infra.git", Narrowed: true,
	}) || !slices.Equal(w.sources.asked, []string{"abc"}) {
		t.Errorf("source = %+v, asked for %v", spec.Source, w.sources.asked)
	}
	if spec.Tool == nil || *spec.Tool != (workersapi.SpecTool{
		Name: "terraform", Version: "1.9.8", Download: true,
	}) || !slices.Equal(w.catalog.asked, []string{"1.9"}) {
		t.Errorf("tool = %+v, asked %v", spec.Tool, w.catalog.asked)
	}
	if !slices.Equal(w.tokens.got, []accessapi.InRunRequest{
		{Account: w.run.Account, Run: w.run.ID, Slug: "infra"},
	}) {
		t.Errorf("in-run tokens minted for %+v", w.tokens.got)
	}
	w.store.run.RuntimeConfig, w.stacks.labels = nil, nil
	w.env.resolved = configapi.Resolved{}
	h, err = w.handover(t, w.run.ID)
	if err != nil || *h.Spec.RunnerImage != "ghcr.io/acme/stack" ||
		*h.Spec.Stack.ProjectRoot != "stack-root" || h.Spec.RuntimeConfig != nil ||
		h.Spec.Stack.Labels == nil || len(h.Spec.Stack.Labels) != 0 {
		t.Errorf("with no runtime configuration = %+v, %v", h.Spec, err)
	}
}

// TestUnitHandoverMetadata checks the account, commit branch and local
// preview variables of a tracked run, a run proposed on a pull request's
// head branch and a run that previews a local workspace, and that a stack
// variable of the same name overrides each.
func TestUnitHandoverMetadata(t *testing.T) {
	for _, tc := range []struct {
		name         string
		typ          kernel.RunType
		branch       string
		localPreview bool
		set          []configapi.Element
		want         map[string]string
	}{
		{
			name: "a tracked run", typ: kernel.RunTypeTracked, branch: "main",
			want: map[string]string{
				"account_name": "acme", "commit_branch": "main",
				"local_preview": "false",
			},
		},
		{
			name: "a run proposed on a pull request", typ: kernel.RunTypeProposed,
			branch: "feature/vpc",
			want: map[string]string{
				"account_name": "acme", "commit_branch": "feature/vpc",
				"local_preview": "false",
			},
		},
		{
			name: "a local preview", typ: kernel.RunTypeProposed, branch: "main",
			localPreview: true,
			want: map[string]string{
				"account_name": "acme", "commit_branch": "main",
				"local_preview": "true",
			},
		},
		{
			name: "stack variables", typ: kernel.RunTypeTracked, branch: "main",
			set: []configapi.Element{
				variable("TF_VAR_infraweave_account_name", "other", false),
				variable("TF_VAR_infraweave_commit_branch", "release", false),
				variable("TF_VAR_infraweave_local_preview", "true", true),
			},
			want: map[string]string{"account_name": "other", "commit_branch": "release"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.store.run.Type, w.store.run.Commit.Branch = tc.typ, tc.branch
			if tc.localPreview {
				w.store.run.LocalWorkspace = "acct/stack/upload"
			}
			w.env.resolved.Elements = tc.set
			h, err := w.handover(t, w.run.ID)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			count := map[string]int{}
			for _, v := range h.Spec.Environment {
				name, ok := strings.CutPrefix(v.Name, "TF_VAR_infraweave_")
				if !ok {
					continue
				}
				count[name]++
				if v.Value != nil {
					got[name] = *v.Value
				}
			}
			for name, want := range tc.want {
				if got[name] != want || count[name] != 1 {
					t.Errorf("%s = %q (%d set), want %q", name, got[name], count[name], want)
				}
			}
			if tc.set != nil && (got["local_preview"] != "" || count["local_preview"] != 1) {
				t.Errorf("a write-only local_preview = %q (%d set)", got["local_preview"],
					count["local_preview"])
			}
		})
	}
}

// TestUnitHandoverOverrides checks that a run's overrides replace the
// stack's variables of their names, unrendered, keeping a write-only
// one sealed; that a template reads the overridden value; that an
// override no variable names is set in the clear; and that a failed read
// of the overrides fails the handover.
func TestUnitHandoverOverrides(t *testing.T) {
	w := newWorld(t)
	w.env.resolved.Elements = []configapi.Element{
		variable("REGION", "eu", false),
		variable("TOKEN", "s3cret", true),
		variable("URL", "https://${{ env.REGION }}.example.com", false),
	}
	w.store.overrides = []lifecycle.Override{
		{Name: "REGION", Value: "${{ us }}"},
		{Name: "TOKEN", Value: "t3st"},
		{Name: "DEBUG", Value: "1"},
	}
	h, err := w.handover(t, w.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	clear, writeOnly := map[string]string{}, map[string]bool{}
	for _, v := range h.Spec.Environment {
		if v.Value != nil {
			clear[v.Name] = *v.Value
		}
		writeOnly[v.Name] = v.WriteOnly
	}
	if clear["REGION"] != "${{ us }}" || clear["DEBUG"] != "1" ||
		clear["URL"] != "https://${{ us }}.example.com" || !writeOnly["TOKEN"] {
		t.Errorf("environment = %v, write-only %v", clear, writeOnly)
	}
	var got sealed
	if err := json.Unmarshal(h.Secrets.Reveal(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Environment["TOKEN"] != "t3st" {
		t.Errorf("the sealed TOKEN = %q", got.Environment["TOKEN"])
	}

	down := errors.New("store down")
	w.store.overrideErr = down
	if _, err := w.handover(t, w.run.ID); !errors.Is(err, down) {
		t.Errorf("a handover whose overrides fail = %v", err)
	}
}

// TestUnitHandoverStateBackend checks that the specification names the
// state backend, StatePath and the stack's ULID, exactly when infraweave
// keeps the stack's state.
func TestUnitHandoverStateBackend(t *testing.T) {
	w := newWorld(t)
	h, err := w.handover(t, w.run.ID)
	if err != nil || h.Spec.StateBackend != nil {
		t.Errorf("a stack without managed state = %+v, %v", h.Spec.StateBackend, err)
	}
	w.stacks.settings.ManagesState = true
	h, err = w.handover(t, w.run.ID)
	want := "/state/v1/stacks/" + w.run.Stack.String()
	if err != nil || h.Spec.StateBackend == nil || h.Spec.StateBackend.Path != want {
		t.Errorf("a stack with managed state = %+v, %v; want %s", h.Spec.StateBackend, err, want)
	}
}

// renderer fails every Check with check and every Evaluate with eval.
type renderer struct{ check, eval error }

func (r renderer) Check(expr.Request) (expr.Check, error) { return expr.Check{}, r.check }

func (r renderer) Evaluate(context.Context, expr.Request, expr.Input) (expr.Result, error) {
	return expr.Result{}, r.eval
}

// TestUnitHandoverRefused checks that a run no account has, or that is not
// on a private pool's worker, is workersapi.ErrNotOnWorker, that a
// template that fails to render is a *kernel.Invalid naming the variable
// and, when it reads a secret, nothing of the failure, and that each
// read's failure fails the handover.
func TestUnitHandoverRefused(t *testing.T) {
	w := newWorld(t)
	if _, err := w.svc.Handover(t.Context(), w.run.ID); !errors.Is(err,
		lifecycle.ErrNoTransaction) {
		t.Errorf("outside a transaction = %v", err)
	}
	if _, err := w.handover(t, kernel.NewULID()); !errors.Is(err, workersapi.ErrNotOnWorker) {
		t.Errorf("no such run = %v", err)
	}
	w.store.run.State = kernel.RunStateFinished
	if _, err := w.handover(t, w.run.ID); !errors.Is(err, workersapi.ErrNotOnWorker) {
		t.Errorf("a finished run = %v", err)
	}
	w.store.run.State, w.store.run.WorkerPool = kernel.RunStatePreparing, nil
	if _, err := w.handover(t, w.run.ID); !errors.Is(err, workersapi.ErrNotOnWorker) {
		t.Errorf("a public pool's run = %v", err)
	}
	w.store.run.WorkerPool = w.run.WorkerPool
	w.env.resolved.Elements = []configapi.Element{
		variable("TOKEN", "s3cret", true),
		variable("BAD", "${{ env.MISSING }}", false),
	}
	_, err := w.handover(t, w.run.ID)
	if inv, ok := errors.AsType[*kernel.Invalid](err); !ok ||
		!strings.Contains(inv.Detail, "BAD") || !strings.Contains(inv.Detail, "MISSING") {
		t.Errorf("a missing variable = %v", err)
	}
	for source, want := range map[string]string{
		"${{ int(env.TOKEN) }}": "The variable BAD's template does not render.",
		"${{ env. }}":           "",
		"${{ 0.0 / 0.0 }}":      "",
	} {
		w.env.resolved.Elements[1] = variable("BAD", source, false)
		_, err := w.handover(t, w.run.ID)
		inv, ok := errors.AsType[*kernel.Invalid](err)
		if !ok || !strings.Contains(inv.Detail, "BAD") || strings.Contains(inv.Detail, "s3cret") ||
			want != "" && inv.Detail != want {
			t.Errorf("%s = %v", source, err)
		}
	}
	w.env.resolved.Elements = []configapi.Element{variable("URL", "${{ 1 }}", false)}
	for name, r := range map[string]handover.Renderer{
		"a check that fails":       renderer{check: expr.ErrUnknownSurface},
		"an evaluation that fails": renderer{eval: context.Canceled},
	} {
		w.svc.BindExpressions(r)
		if _, err := w.handover(t, w.run.ID); err == nil || errors.As(err, new(*kernel.Invalid)) {
			t.Errorf("%s = %v", name, err)
		}
	}
	w.svc.BindExpressions(nil)
	if _, err := w.handover(t, w.run.ID); err == nil {
		t.Error("a template with no registry bound rendered")
	}
	w.env.resolved.Elements = nil
	w.store.run.RuntimeConfig = json.RawMessage(`[`)
	if _, err := w.handover(t, w.run.ID); err == nil {
		t.Error("a runtime configuration that does not decode was handed over")
	}
	w.store.run.RuntimeConfig = nil
	for name, fail := range map[string]func(bool){
		"the run":         func(on bool) { w.store.err = cond(on) },
		"the settings":    func(on bool) { w.stacks.fail = map[bool]string{true: "Settings"}[on] },
		"the labels":      func(on bool) { w.stacks.fail = map[bool]string{true: "Labels"}[on] },
		"the spaces":      func(on bool) { w.spaces.err = cond(on) },
		"the account":     func(on bool) { w.accounts.err = cond(on) },
		"the environment": func(on bool) { w.env.err = cond(on) },
		"the token":       func(on bool) { w.tokens.err = cond(on) },
		"the source":      func(on bool) { w.sources.err = cond(on) },
		"the catalogue":   func(on bool) { w.catalog.err = cond(on) },
		"the key":         func(on bool) { w.store.keyErr = cond(on) },
	} {
		fail(true)
		if _, err := w.handover(t, w.run.ID); !errors.Is(err, errDown) {
			t.Errorf("%s down = %v", name, err)
		}
		fail(false)
	}
}

// cond is errDown when on.
func cond(on bool) error {
	if on {
		return errDown
	}
	return nil
}

// TestUnitHandoverLocalPreview checks that a local preview's source is a
// signed GET of its uploaded archive, with no credential and no Git source
// asked for, even before sources are bound; that an expired upload is a
// *kernel.Invalid saying so; and that a failed signing fails the
// handover.
func TestUnitHandoverLocalPreview(t *testing.T) {
	w := newWorld(t)
	w.store.run.Type, w.store.run.LocalWorkspace = kernel.RunTypeProposed, "acct/stack/upload"
	w.svc = handover.New(handover.Deps{
		Store: w.store, Stacks: w.stacks, Spaces: w.spaces, Accounts: w.accounts,
		Environment: w.env, Tokens: w.tokens, Catalog: w.catalog, Objects: w.objects,
	})
	w.svc.BindExpressions(registry(t))
	h, err := w.handover(t, w.run.ID)
	if err != nil || h.Spec.Source != (workersapi.SpecSource{
		Kind: workersapi.SourceArchive, URL: "https://store.test/uploads/acct/stack/upload",
	}) || len(w.sources.asked) != 0 {
		t.Fatalf("a local preview = %+v, %v; asked %v", h.Spec.Source, err, w.sources.asked)
	}
	if strings.Contains(string(h.Secrets.Reveal()), "sourceCredential") {
		t.Errorf("a local preview's secrets name a credential: %s", h.Secrets.Reveal())
	}
	w.objects.live = nil
	if _, err := w.handover(t, w.run.ID); err == nil ||
		!strings.Contains(err.Error(), "local workspace has expired") {
		t.Errorf("an expired upload = %v", err)
	}
	w.objects.err = errDown
	if _, err := w.handover(t, w.run.ID); !errors.Is(err, errDown) {
		t.Errorf("a failed signing = %v", err)
	}
}

// TestUnitHandoverSourceAndTool checks a raw Git source, with no
// credential and not narrowed; an OpenTofu stack's tofu at the release
// its terraformVersion resolves to, over its settings' version, run from
// the image when the stack skips the download; masking as the stack sets
// it; no tool for a vendor that mounts none; that a constraint no release
// meets is a *kernel.Invalid naming the setting it comes from, and a
// catalogue that lists none one naming /terraformVersion; and that a
// handover refused for its tool, or with no sources bound, mints neither
// the source's token nor the in-run API token.
func TestUnitHandoverSourceAndTool(t *testing.T) {
	w := newWorld(t)
	w.sources.src = runsapi.Source{URL: "https://example.com/infra.git"}
	constraint := " ~> 1.9 "
	w.stacks.settings.Vendor, w.stacks.settings.TerraformVersion = "opentofu", &constraint
	w.stacks.settings.SkipVendorBinaryDownload = true
	w.stacks.settings.EnableWellKnownSecretMasking = true
	h, err := w.handover(t, w.run.ID)
	if err != nil || h.Spec.Source.Narrowed ||
		h.Spec.Source.URL != "https://example.com/infra.git" ||
		h.Spec.Tool == nil ||
		*h.Spec.Tool != (workersapi.SpecTool{Name: "tofu", Version: "1.9.8"}) ||
		!slices.Equal(w.catalog.asked, []string{" ~> 1.9 "}) ||
		!h.Spec.Stack.EnableWellKnownSecretMasking {
		t.Fatalf("an OpenTofu stack on raw Git = %+v, %v; asked %v", h.Spec, err, w.catalog.asked)
	}
	if strings.Contains(string(h.Secrets.Reveal()), "sourceCredential") {
		t.Errorf("raw Git's secrets name a credential: %s", h.Secrets.Reveal())
	}

	w.stacks.settings.Vendor = "pulumi"
	if h, err = w.handover(t, w.run.ID); err != nil || h.Spec.Tool != nil {
		t.Errorf("a vendor with no tool = %+v, %v", h.Spec.Tool, err)
	}

	w.stacks.settings.Vendor = "terraform"
	asked, minted := len(w.sources.asked), len(w.tokens.got)
	for _, tc := range []struct {
		name, pointer string
		set           func()
	}{
		{"a constraint no release meets", "/terraformVersion", func() { w.catalog.invalid = true }},
		{
			"a settings' version no release meets", "/vendorConfig/terraform/version",
			func() { w.stacks.settings.TerraformVersion = nil },
		},
		{"a catalogue that lists none", "/terraformVersion", func() {
			w.catalog.invalid, w.stacks.settings.VendorConfig = false, nil
			w.catalog.versions = nil
		}},
	} {
		tc.set()
		_, err := w.handover(t, w.run.ID)
		inv, ok := errors.AsType[*kernel.Invalid](err)
		if !ok || len(inv.Errors) != 1 || inv.Errors[0].Pointer != tc.pointer {
			t.Errorf("%s = %v", tc.name, err)
		}
	}
	if len(w.sources.asked) != asked || len(w.tokens.got) != minted {
		t.Errorf("a refused tool minted: sources %v, tokens %v", w.sources.asked, w.tokens.got)
	}

	w.catalog.versions = []string{"1.14.2"}
	w.svc.BindSources(nil)
	if _, err := w.handover(t, w.run.ID); err == nil || len(w.tokens.got) != minted {
		t.Errorf("no sources bound = %v, tokens %v", err, w.tokens.got)
	}
}
