package handover

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/surface"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// WorkspaceRoot is where a run's workspace is mounted in its container.
//
// ref: decision 0008 § "The launcher and the runner"
const WorkspaceRoot = "/mnt/workspace"

// Store is what a handover needs of the runs' storage. Each method joins
// the transaction its context carries.
type Store interface {
	// Handed reads the run id of whichever account has it and locks its
	// row; lifecycle.ErrNotFound when none has it.
	Handed(ctx context.Context, id kernel.ULID) (lifecycle.Run, error)
	// ArtifactKey is run's artifact key, made and stored sealed the first
	// time it is asked for.
	ArtifactKey(ctx context.Context, run lifecycle.Run) (crypt.Value, error)
}

// Tokens mints a run's in-run API token; the access domain's Tokens is
// one.
type Tokens interface {
	InRunToken(ctx context.Context, r accessapi.InRunRequest) (accessapi.Token, error)
}

// Renderer checks and renders templates; the expression registry is one.
type Renderer interface {
	Check(req expr.Request) (expr.Check, error)
	Evaluate(ctx context.Context, req expr.Request, in expr.Input) (expr.Result, error)
}

// Deps are what a handover reads: the runs' storage, the run's stack, the
// slugs of its account and the account's spaces, the stack's resolved
// environment, and the tokens it mints.
type Deps struct {
	Store       Store
	Stacks      stacksapi.Stacks
	Spaces      accessapi.Spaces
	Accounts    accessapi.Accounts
	Environment configapi.Environment
	Tokens      Tokens
}

// Service builds the specifications of claimed runs.
type Service struct {
	store    Store
	stacks   stacksapi.Stacks
	spaces   accessapi.Spaces
	accounts accessapi.Accounts
	env      configapi.Environment
	tokens   Tokens
	exprs    Renderer
}

// New returns the service over d.
func New(d Deps) *Service {
	return &Service{
		store: d.Store, stacks: d.Stacks, spaces: d.Spaces, accounts: d.Accounts,
		env: d.Environment, tokens: d.Tokens,
	}
}

// BindExpressions binds the expression registry, which internal/app
// builds after it constructs the domains that declare surfaces. Until it
// is bound, a value with a template fails its handover.
func (s *Service) BindExpressions(r Renderer) { s.exprs = r }

// errNoRenderer is a template to render before BindExpressions.
var errNoRenderer = errors.New("runs: no expression registry is bound")

// secrets is the document a Handover's Secrets holds, the worker API's
// RunSpecSecrets.
type secrets struct {
	Environment       map[string]string `json:"environment"`
	Files             map[string][]byte `json:"files"`
	APIToken          string            `json:"apiToken"`
	APITokenExpiresAt int64             `json:"apiTokenExpiresAt"`
	ArtifactKey       []byte            `json:"artifactKey"`
}

// runtimeConfig is what a handover reads of a trigger's runtime
// configuration itself; the runner applies the rest.
type runtimeConfig struct {
	RunnerImage *string `json:"runnerImage"`
	ProjectRoot *string `json:"projectRoot"`
}

// Handover builds run's specification, as workersapi.Scheduler's Handover
// says, in the transaction ctx carries. It opens the stack's environment,
// renders each variable's ${{ … }} templates against the environment
// surface, and puts each write-only value, and each value whose template
// reads one, in the secrets with the in-run API token it mints and the
// run's artifact key. A template renders against the variables as saved,
// at the time the run entered its state and with draws seeded by the
// run's ID, so every handover of the run in one state renders alike.
//
// ref: decision 0008 § "The run specification and its secrets"; decision
// 0024 "Environment interpolation"
func (s *Service) Handover(ctx context.Context, id kernel.ULID) (workersapi.Handover, error) {
	if !tx.In(ctx) {
		return workersapi.Handover{}, lifecycle.ErrNoTransaction
	}
	r, err := s.store.Handed(ctx, id)
	switch {
	case errors.Is(err, lifecycle.ErrNotFound):
		return workersapi.Handover{}, fmt.Errorf("%w: %s", workersapi.ErrNotOnWorker, id)
	case err != nil:
		return workersapi.Handover{}, err //nolint:wrapcheck // the store names the read
	case r.WorkerPool == nil || !r.State.OnWorker():
		return workersapi.Handover{}, fmt.Errorf("%w: run %s is %s", workersapi.ErrNotOnWorker,
			id, r.State)
	}
	st, err := s.stacks.Settings(ctx, r.Account, r.Stack)
	if err != nil {
		return workersapi.Handover{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	labels, err := s.stacks.Labels(ctx, r.Account, []kernel.ULID{r.Stack})
	if err != nil {
		return workersapi.Handover{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	slugs, err := s.spaces.Slugs(ctx, r.Account)
	if err != nil {
		return workersapi.Handover{}, err //nolint:wrapcheck // the access domain names the read
	}
	account, err := s.accounts.Slug(ctx, r.Account)
	if err != nil {
		return workersapi.Handover{}, err //nolint:wrapcheck // the access domain names the read
	}
	resolved, err := s.env.Resolve(ctx, r.Account, r.Stack)
	if err != nil {
		return workersapi.Handover{}, err //nolint:wrapcheck // the configuration domain names it
	}
	var rc runtimeConfig
	if len(r.RuntimeConfig) > 0 {
		if err := json.Unmarshal(r.RuntimeConfig, &rc); err != nil {
			return workersapi.Handover{}, fmt.Errorf(
				"runs: decode run %s's runtime configuration: %w",
				r.ID,
				err,
			)
		}
	}
	stack := workersapi.SpecStack{
		Slug: st.Slug, Name: st.Name, Space: slugs[st.Space],
		Labels: nonNil(labels[r.Stack]), Repository: st.Repository, Branch: st.Branch,
		ProjectRoot: cmp.Or(rc.ProjectRoot, st.ProjectRoot), Vendor: st.Vendor,
		VendorConfig: st.VendorConfig,
	}
	spec := workersapi.Spec{
		Run: workersapi.SpecRun{
			ID: r.ID, Type: r.Type, State: r.State, StateVersion: r.Version,
			TriggeredBy: r.TriggeredBy, Priority: r.Priority, DriftDetection: r.DriftDetection,
			CreatedAt: r.CreatedAt,
		},
		Stack: stack,
		Commit: workersapi.SpecCommit{
			Hash: r.Commit.Hash, Tag: r.Commit.Tag, URL: r.Commit.URL, Message: r.Commit.Message,
			AuthorName: r.Commit.AuthorName, AuthorLogin: r.Commit.AuthorLogin,
			Timestamp: r.Commit.Timestamp,
		},
		RuntimeConfig: r.RuntimeConfig,
		RunnerImage:   cmp.Or(rc.RunnerImage, st.RunnerImage),
		Hooks:         resolved.Hooks,
	}
	sec := secrets{Environment: map[string]string{}, Files: map[string][]byte{}}
	if err := s.environment(ctx, r, stack, account, resolved.Elements, &spec, &sec); err != nil {
		return workersapi.Handover{}, err
	}
	tok, err := s.tokens.InRunToken(ctx, accessapi.InRunRequest{
		Account: r.Account, Run: r.ID, Slug: st.Slug,
	})
	if err != nil {
		return workersapi.Handover{}, err //nolint:wrapcheck // the access domain names the mint
	}
	key, err := s.store.ArtifactKey(ctx, r)
	if err != nil {
		return workersapi.Handover{}, err //nolint:wrapcheck // the store names the key
	}
	sec.APIToken, sec.APITokenExpiresAt = tok.Raw, tok.Expires.Unix()
	sec.ArtifactKey = key.Reveal()
	// A document of strings, byte slices and an integer always encodes.
	doc, _ := json.Marshal(sec) //nolint:gosec // the secrets, held only sealed to the pool
	return workersapi.Handover{
		Account: r.Account, Pool: *r.WorkerPool, Spec: spec, Secrets: crypt.NewValue(doc),
	}, nil
}

// environment fills spec's variables and files from elements and
// infraweave's own variables of r on stack in account, by name and by
// path, and sec with the write-only values.
func (s *Service) environment(
	ctx context.Context,
	r lifecycle.Run,
	stack workersapi.SpecStack,
	account string,
	elements []configapi.Element,
	spec *workersapi.Spec,
	sec *secrets,
) error {
	env, writeOnly := map[string]any{}, map[string]bool{}
	for _, e := range elements {
		if !e.FileMount {
			env[e.Name], writeOnly[e.Name] = string(reveal(e.Value)), e.WriteOnly
		}
	}
	in := expr.Input{
		Now:  r.StateChangedAt,
		Seed: binary.BigEndian.Uint64(r.ID[len(r.ID)-8:]),
		Values: map[string]any{
			configapi.EnvVariable: env, configapi.StackVariable: stackValue(stack),
			surface.Name: surface.Value(r),
		},
	}
	for _, e := range elements {
		if e.FileMount {
			f := workersapi.File{
				Path: e.Name, Mode: e.FileMode, Checksum: e.Checksum, WriteOnly: e.WriteOnly,
			}
			if e.WriteOnly {
				sec.Files[e.Name] = reveal(e.Value)
			} else {
				f.Content = reveal(e.Value)
			}
			spec.Files = append(spec.Files, f)
			continue
		}
		v, secret, err := s.render(ctx, e.Name, string(reveal(e.Value)), in, writeOnly)
		if err != nil {
			return err
		}
		if e.WriteOnly || secret {
			sec.Environment[e.Name] = v
			spec.Environment = append(spec.Environment,
				workersapi.Variable{Name: e.Name, WriteOnly: true})
			continue
		}
		spec.Environment = append(spec.Environment, workersapi.Variable{Name: e.Name, Value: &v})
	}
	for _, v := range own(r, stack, account) {
		if _, ok := writeOnly[v.Name]; !ok {
			spec.Environment = append(spec.Environment, v)
		}
	}
	slices.SortFunc(spec.Environment, func(a, b workersapi.Variable) int {
		return strings.Compare(a.Name, b.Name)
	})
	slices.SortFunc(spec.Files, func(a, b workersapi.File) int {
		return strings.Compare(a.Path, b.Path)
	})
	return nil
}

// render is source with its ${{ … }} templates rendered against in, and
// whether it reads a variable writeOnly marks, which makes it a secret.
// A template that fails to render is a *kernel.Invalid naming the
// variable, whose message is left out when the template reads a secret,
// since a failure can quote the value it read.
func (s *Service) render(
	ctx context.Context,
	name, source string,
	in expr.Input,
	writeOnly map[string]bool,
) (string, bool, error) {
	if !strings.Contains(source, "${{") {
		return source, false, nil
	}
	if s.exprs == nil {
		return "", false, errNoRenderer
	}
	req := expr.Request{Surface: configapi.Surface, Form: expr.FormTemplate, Source: source}
	chk, err := s.exprs.Check(req)
	if err != nil {
		return "", false, fmt.Errorf("runs: check variable %s's template: %w", name, err)
	}
	secret := slices.ContainsFunc(chk.Reads, func(rd expr.Read) bool {
		return rd.Variable == configapi.EnvVariable && writeOnly[rd.Key]
	})
	res, err := s.exprs.Evaluate(ctx, req, in)
	if err == nil {
		var out string
		if out, err = text(res.Value); err == nil {
			return out, secret, nil
		}
	}
	_, failed := errors.AsType[*expr.Failed](err)
	_, broken := errors.AsType[*expr.CheckError](err)
	if !failed && !broken && !errors.Is(err, errText) {
		return "", false, fmt.Errorf("runs: render variable %s: %w", name, err)
	}
	detail := fmt.Sprintf("The variable %s's template does not render", name)
	if secret {
		return "", false, &kernel.Invalid{Detail: detail + "."}
	}
	return "", false, &kernel.Invalid{Detail: detail + ": " + err.Error()}
}

// errText is a template's value that has no text, such as NaN.
var errText = errors.New("its value has no text")

// text is a rendered value as an environment variable holds it: a string
// as it is, and any other value, which a template that is one whole
// expression keeps, as JSON.
func text(v any) (string, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", errText
	}
	return string(b), nil
}

// own are infraweave's own variables of run r on stack in the account
// whose slug is account, which the server knows at handover.
// TF_VAR_infraweave_run_state changes with each phase, so the runner sets
// it.
//
// ref: decision 0008 § "The run specification and its secrets"
func own(r lifecycle.Run, stack workersapi.SpecStack, account string) []workersapi.Variable {
	root := ""
	if stack.ProjectRoot != nil {
		root = *stack.ProjectRoot
	}
	// A list of strings always encodes, and its JSON is an HCL list
	// literal.
	list, _ := json.Marshal(stack.Labels)
	vars := map[string]string{
		"account_name":      account,
		"commit_branch":     r.Commit.Branch,
		"commit_sha":        r.Commit.Hash,
		"local_preview":     strconv.FormatBool(r.LocalPreview),
		"project_root":      root,
		"repository":        stack.Repository,
		"run_id":            r.ID.String(),
		"run_trigger":       r.TriggeredBy,
		"run_type":          string(r.Type),
		"space_id":          stack.Space,
		"stack_branch":      stack.Branch,
		"stack_id":          stack.Slug,
		"stack_labels":      strings.Join(stack.Labels, ","),
		"stack_labels_list": string(list),
		"workspace_root":    WorkspaceRoot,
	}
	out := make([]workersapi.Variable, 0, len(vars))
	for k, v := range vars {
		out = append(out, workersapi.Variable{Name: "TF_VAR_infraweave_" + k, Value: &v})
	}
	return out
}

// stackValue is stack as a template reads it, the environment surface's
// StackVariable.
func stackValue(stack workersapi.SpecStack) map[string]any {
	root := ""
	if stack.ProjectRoot != nil {
		root = *stack.ProjectRoot
	}
	labels := make([]any, 0, len(stack.Labels))
	for _, l := range stack.Labels {
		labels = append(labels, l)
	}
	return map[string]any{
		"id": stack.Slug, "name": stack.Name, "space": stack.Space, "labels": labels,
		"repository": stack.Repository, "branch": stack.Branch, "projectRoot": root,
	}
}

// reveal is v's bytes, none when the element has no value.
func reveal(v configapi.Secret) []byte {
	if v == nil {
		return nil
	}
	return v.Reveal()
}

// nonNil is s, empty rather than nil.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
