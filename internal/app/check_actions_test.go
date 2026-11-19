package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsassembly "github.com/weaveplatform/infraweave/internal/vcs/assembly"
	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/test/deps"
)

type checkActionApp struct {
	holdWorld
	cfg         config.Config
	fake        *githubfake.Fake
	app         githubfake.App
	box         *outbox.Outbox
	ingress     *httptest.Server
	jobs        *queue.Queue
	stack       kernel.ULID
	integration string
	head, main  string
	check       int64
	logs        bytes.Buffer
}

func newCheckActionApp(t *testing.T) *checkActionApp {
	t.Helper()
	f := githubfake.New()
	srv := f.Start()
	t.Cleanup(srv.Close)
	a := f.AddApp("check-actions", "acme")
	f.Install(a.ID, "acme", "Organization", "infra", "other")
	repo := f.Repo("acme", "infra")
	head := repo.Commit("main", "check commit", map[string]string{"main.tf": "terraform {}\n"})
	main := repo.Commit("main", "newer tracked commit", map[string]string{"main.tf": "terraform { required_version = \">= 1.0\" }\n"})
	ca := filepath.Join(t.TempDir(), "github-ca.pem")
	if err := os.WriteFile(ca, srv.CA, 0o600); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.VCS.CAFile = ca
	h := newHoldWorldConfig(t, c)
	w := &checkActionApp{holdWorld: h, cfg: c, fake: f, app: a, head: head, main: main, jobs: queue.New(h.q)}
	principal, ok := kernel.PrincipalFrom(w.admin)
	if !ok {
		t.Fatal("missing fixture principal")
	}
	principal.Login = "alice"
	w.admin = kernel.WithPrincipal(w.admin, principal)
	w.box = testEvents(t, h.q)
	var err error
	w.d, err = newDomains(c, h.db, h.keys, h.blobs.Objects, nil, w.box, slog.New(slog.NewJSONHandler(&w.logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	created, err := w.d.VCS.API().GithubEnterpriseIntegrationCreate(w.admin, api.GithubEnterpriseIntegrationCreateRequestObject{Body: &api.GithubEnterpriseIntegrationCreateRequest{
		ApiHost: srv.URL, AppID: strconv.FormatInt(a.ID, 10), PrivateKey: string(a.PrivateKey), WebhookSecret: &a.WebhookSecret,
		CustomInput: &api.CustomVCSInput{Name: "Check actions", SpaceID: "hold", IsDefault: new(true)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	response, ok := created.(api.GithubEnterpriseIntegrationCreate201JSONResponse)
	if !ok {
		t.Fatalf("integration creation answered %T", created)
	}
	w.integration = response.Body.Id
	_, err = w.d.Stacks.API().StackCreate(w.admin, api.StackCreateRequestObject{Body: &api.StackCreateRequest{Input: api.StackInput{
		Name:               "Actions",
		Repository:         "infra",
		Namespace:          new("acme"),
		Branch:             "main",
		Provider:           new(api.VCSProviderGITHUBENTERPRISE),
		Space:              new("hold"),
		VcsIntegrationId:   &w.integration,
		GithubActionDeploy: new(true),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	stack, err := w.d.Stacks.Stacks().BySlug(t.Context(), w.acct, "actions")
	if err != nil {
		t.Fatal(err)
	}
	w.stack = stack.ID
	if err := tx.Do(t.Context(), w.q, tx.Options{}, func(ctx context.Context) error {
		_, err := w.d.Runs.Creator().
			Create(ctx, runsapi.CreateRequest{Account: w.acct, Stack: w.stack, Type: kernel.RunTypeProposed, Commit: runsapi.Commit{Hash: head}, TriggeredBy: "alice", Mode: runsapi.None})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	reg := queue.NewRegistry()
	if err := w.box.Register(reg); err != nil {
		t.Fatal(err)
	}
	check := w.registration(t, "vcs.report_check")
	if err := reg.Register(check.Kind, check.Handler, check.Options); err != nil {
		t.Fatal(err)
	}
	cfg := queue.Defaults()
	cfg.PollInterval = 10 * time.Millisecond
	workerCtx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	workers := queue.NewWorkers(w.jobs, reg, cfg, queue.AllowAll{}, nil, discard(), "check-action-bootstrap")
	go func() { defer close(done); workers.Run(workerCtx) }()
	t.Cleanup(func() { stop(); <-done })
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := deps.Eventually(ctx, "real source tracking and queued run check", func(ctx context.Context) (bool, error) {
		current, err := w.d.Stacks.Stacks().Settings(ctx, w.acct, w.stack)
		if err != nil {
			return false, err
		}
		if current.TrackedCommit == nil || current.TrackedCommit.Hash != main {
			return false, nil
		}
		for _, check := range f.Checks("acme", "infra") {
			if check.ExternalID == w.stack.String() && check.SHA == head {
				w.check = check.ID
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	stop()
	<-done
	w.serve(t)
	return w
}

func (w *checkActionApp) serve(t *testing.T) {
	t.Helper()
	if w.ingress != nil {
		w.ingress.Close()
	}
	r := chi.NewRouter()
	w.d.VCS.Routes(r)
	w.ingress = httptest.NewServer(r)
	t.Cleanup(w.ingress.Close)
}

func (w *checkActionApp) registration(t *testing.T, name string) queue.Registration {
	t.Helper()
	for _, registration := range w.d.VCS.Jobs() {
		if registration.Kind.Name == name {
			return registration
		}
	}
	t.Fatal("missing actual VCS queue registration", name)
	return queue.Registration{}
}

func (w *checkActionApp) claim(t *testing.T, name string) queue.Job {
	t.Helper()
	registration := w.registration(t, name)
	jobs, err := w.jobs.Claim(t.Context(), registration.Kind.Queue, []string{name}, 1, time.Minute, "check-action-app")
	if err != nil || len(jobs) != 1 {
		t.Fatal("missing real claimed work", name, len(jobs), err)
	}
	return jobs[0]
}

func (w *checkActionApp) press(t *testing.T, id, action string, want int) {
	t.Helper()
	status, err := w.fake.PressCheck(t.Context(), w.ingress.URL+"/webhooks/vcs/github_enterprise/"+w.integration, id, "acme", "infra", w.check, action, "developer")
	if err != nil || status != want {
		t.Fatal("signed recorded-check press", status, err)
	}
}

func (w *checkActionApp) process(t *testing.T) {
	t.Helper()
	job := w.claim(t, "vcs.process_delivery")
	if err := w.registration(t, job.Kind).Handler(t.Context(), job); err != nil {
		t.Fatal(err)
	}
}

func (w *checkActionApp) runViews(t *testing.T) []api.Run {
	t.Helper()
	result, err := w.d.Runs.API().StackRuns(w.admin, api.StackRunsRequestObject{StackId: "actions"})
	if err != nil {
		t.Fatal(err)
	}
	response, ok := result.(api.StackRuns200JSONResponse)
	if !ok {
		t.Fatalf("run read answered %T", result)
	}
	proposed, err := w.d.Runs.API().StackProposedRuns(w.admin, api.StackProposedRunsRequestObject{StackId: "actions"})
	if err != nil {
		t.Fatal(err)
	}
	proposedResponse, ok := proposed.(api.StackProposedRuns200JSONResponse)
	if !ok {
		t.Fatalf("proposed read answered %T", proposed)
	}
	return append(response.Body.Items, proposedResponse.Body.Items...)
}

func (w *checkActionApp) pointers(t *testing.T) []byte {
	t.Helper()
	settings, err := w.d.Stacks.Stacks().Settings(t.Context(), w.acct, w.stack)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(struct {
		Head    *stacksapi.Commit `json:"head"`
		Tracked *stacksapi.Commit `json:"tracked"`
		By      string            `json:"by"`
	}{settings.TrackedBranchHead, settings.TrackedCommit, settings.TrackedCommitSetBy})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func (w *checkActionApp) promotion(t *testing.T, enabled bool) {
	t.Helper()
	patch := api.MergePatch{"githubActionDeploy": enabled}
	result, err := w.d.Stacks.API().StackPatch(w.admin, api.StackPatchRequestObject{StackId: "actions", Body: &patch})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.(api.StackPatch200JSONResponse); !ok {
		t.Fatalf("promotion patch answered %T", result)
	}
}

func TestAccGitHubCheckActionsCreateFreshRuns(t *testing.T) {
	for _, action := range []string{"preview", "deploy"} {
		t.Run(action, func(t *testing.T) {
			w := newCheckActionApp(t)
			w.fake.SetCollaboratorPermission("acme", "infra", "developer", "write")
			before := w.pointers(t)
			for _, id := range []string{"first-press", "intentional-repeat"} {
				w.press(t, id, action, 202)
				w.process(t)
				job := w.claim(t, "vcs.evaluate_event")
				if err := w.registration(t, job.Kind).Handler(t.Context(), job); err != nil {
					t.Fatal(err)
				}
				if err := w.jobs.Complete(t.Context(), job); !errors.Is(err, queue.ErrLeaseLost) {
					t.Fatal("action and queue did not complete atomically", err)
				}
				w.press(t, id, action, 200)
			}
			runs := w.runViews(t)
			want := api.RunTypePROPOSED
			if action == "deploy" {
				want = api.RunTypeTRACKED
			}
			count := 0
			for _, run := range runs {
				if run.TriggeredBy != nil && *run.TriggeredBy == "github/developer" {
					if run.Type != want || run.Commit.Hash != w.head {
						t.Fatal("button lost run type/immutable commit", run.Id, run.Type)
					}
					count++
				}
			}
			if len(runs) != 3 || count != 2 || !bytes.Equal(before, w.pointers(t)) {
				t.Fatal("duplicate/repeat or tracked pointer contract", len(runs), count)
			}
		})
	}
}

func TestAccGitHubCheckActionCurrentPermissionAndSetting(t *testing.T) {
	for _, scenario := range []string{"permission revoked", "setting off before parent", "setting off before child"} {
		t.Run(scenario, func(t *testing.T) {
			w := newCheckActionApp(t)
			w.fake.SetCollaboratorPermission("acme", "infra", "developer", "write")
			before := w.pointers(t)
			if scenario == "setting off before parent" {
				w.promotion(t, false)
				w.fake.Fail(githubfake.ServerError, 0)
			}
			calls := w.fake.Requests()
			w.press(t, "refused-press", "deploy", 202)
			w.process(t)
			if scenario == "setting off before parent" {
				if w.fake.Requests() != calls {
					t.Fatal("disabled forged press made native request")
				}
			} else {
				if scenario == "permission revoked" {
					w.fake.SetCollaboratorPermission("acme", "infra", "developer", "read")
				} else {
					w.promotion(t, false)
					w.fake.Fail(githubfake.ServerError, 0)
				}
				calls = w.fake.Requests()
				job := w.claim(t, "vcs.evaluate_event")
				if err := w.registration(t, job.Kind).Handler(t.Context(), job); err != nil {
					t.Fatal(err)
				}
				if scenario == "setting off before child" && w.fake.Requests() != calls {
					t.Fatal("disabled child made native request")
				}
			}
			if len(w.runViews(t)) != 1 || !bytes.Equal(before, w.pointers(t)) {
				t.Fatal("refused action created a run or moved source pointers")
			}
		})
	}
}

func TestAccGitHubCheckActionOldClaimCannotCreate(t *testing.T) {
	w := newCheckActionApp(t)
	w.press(t, "generation", "preview", 202)
	w.process(t)
	old := w.claim(t, "vcs.evaluate_event")
	if err := w.jobs.Extend(t.Context(), old, -time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := w.jobs.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	current := w.claim(t, "vcs.evaluate_event")
	calls := w.fake.Requests()
	if err := w.registration(t, old.Kind).Handler(t.Context(), old); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatal("old claim crossed creation fence", err)
	}
	if w.fake.Requests() != calls || len(w.runViews(t)) != 1 {
		t.Fatal("stale generation reached provider or created")
	}
	if err := w.registration(t, current.Kind).Handler(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	if len(w.runViews(t)) != 2 {
		t.Fatal("current genuine claim failed to recover")
	}
}

type rollbackActionCreator struct {
	runsapi.Creator
	cause error
}

func (c *rollbackActionCreator) Create(ctx context.Context, in runsapi.CreateRequest) (runsapi.CreatedRun, error) {
	run, err := c.Creator.Create(ctx, in)
	if err != nil {
		return run, err
	}
	return run, c.cause
}

func TestAccGitHubCheckActionCreationRollback(t *testing.T) {
	w := newCheckActionApp(t)
	base := w.d
	creator := &rollbackActionCreator{Creator: base.Runs.Creator(), cause: errors.New("completion unavailable")}
	// Reconstruct only VCS at the app's public composition seam. Every forward
	// port and the wrapped Creator still uses its genuine domain implementation.
	domain, err := vcsassembly.New(vcsassembly.Deps{
		DB: w.q, Keys: w.keys, Spaces: base.Access.Spaces(), Authz: base.Access.Authorizer(), Stacks: base.Stacks.Stacks(),
		Histories: base.Runs, PlanningLogs: base.Runs.PlanningLogs(), CommitRuns: base.Runs.CommitRuns(), CheckFacts: base.Runs.CheckFacts(),
		Settings: w.cfg.VCS, Public: w.cfg.Server.PublicURL, Log: discard(), Objects: w.blobs.Objects,
		PushEventTimeout: w.cfg.Policy.Timeouts.PushEvent, Features: base.Features.Entitlements(), Creator: creator, Supersede: base.Runs.Supersede(),
	})
	if err != nil {
		t.Fatal(err)
	}
	w.d.VCS = domain
	w.serve(t)
	if _, err := domain.API().
		AccountUpdateVCSEventTriggeredRunsLimit(w.admin, api.AccountUpdateVCSEventTriggeredRunsLimitRequestObject{Body: &api.AccountUpdateVCSEventTriggeredRunsLimitRequest{Limit: new(int32(1))}}); err != nil {
		t.Fatal(err)
	}
	before := w.pointers(t)
	w.press(t, "rollback", "preview", 202)
	w.process(t)
	job := w.claim(t, "vcs.evaluate_event")
	if err := w.registration(t, job.Kind).Handler(t.Context(), job); !errors.Is(err, creator.cause) {
		t.Fatal("genuine creation failure lost", err)
	}
	if len(w.runViews(t)) != 1 || !reflect.DeepEqual(before, w.pointers(t)) {
		t.Fatal("failed completion leaked a run/source mutation")
	}
	if err := tx.Do(t.Context(), w.q, tx.Options{}, func(ctx context.Context) error { return w.jobs.HoldTx(ctx, job) }); err != nil {
		t.Fatal("failed creation acknowledged claim", err)
	}
	creator.cause = nil
	if err := w.registration(t, job.Kind).Handler(t.Context(), job); err != nil {
		t.Fatal("same claim/cap reservation did not recover", err)
	}
	if len(w.runViews(t)) != 2 || !bytes.Equal(before, w.pointers(t)) {
		t.Fatal("recovery lost exactly-one creation/pointer invariance")
	}
}
