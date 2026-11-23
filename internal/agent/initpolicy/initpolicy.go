//nolint:tagliatelle // Worker initialization JSON uses the specified version-1 policy input spelling.
package initpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/platform/mask"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

// DefaultTimeout bounds the whole worker-local admission, including queue waits.
const DefaultTimeout = 60 * time.Second

// Settings identifies a local source file and its explicit Rego dialect/budget.
type Settings struct {
	Path    string
	Engine  rego.EngineType
	Timeout time.Duration
}

// ErrSettings refuses an invalid launcher initialization setting.
var ErrSettings = errors.New("invalid launcher initialization policy configuration")

// Validate rejects unknown dialects and budgets beyond the worker bound.
func (s Settings) Validate() error {
	if s.Engine != rego.V0 && s.Engine != rego.V1 || s.Timeout <= 0 || s.Timeout > DefaultTimeout {
		return ErrSettings
	}
	if s.Path != "" && strings.TrimSpace(s.Path) == "" {
		return ErrSettings
	}
	return nil
}

var (
	// ErrPolicy marks unreadable, oversized or invalid startup policy source.
	ErrPolicy = errors.New("worker initialization policy could not be loaded")
	// ErrEvaluation hides raw OPA diagnostics and input on a failed admission.
	ErrEvaluation = errors.New("worker initialization policy evaluation failed")
	// ErrInput refuses incomplete or inconsistent assignment facts.
	ErrInput = errors.New("invalid worker initialization input")
)

type failure struct {
	kind  error
	cause error
}

// Error is the kind, and for an evaluation an exec call stopped, the
// call's reason, whose text names no argument, output or image.
func (f *failure) Error() string {
	var command *rego.CommandError
	if errors.As(f.cause, &command) {
		return f.kind.Error() + ": " + command.Error()
	}
	return f.kind.Error()
}
func (f *failure) Unwrap() []error { return []error{f.kind, f.cause} }

// Policy is a checked startup snapshot. It retains no run input or credentials.
type Policy struct {
	engine  *rego.Engine
	policy  rego.Policy
	timeout time.Duration
}

// Load returns nil when disabled. A configured file must be a bounded regular
// file and compile successfully before the worker can register.
func Load(ctx context.Context, settings Settings) (*Policy, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if settings.Path == "" {
		return nil, nil //nolint:nilnil // no configured policy
	}
	ctx, cancel := context.WithTimeout(ctx, settings.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, &failure{ErrPolicy, err}
	}
	info, err := os.Stat(settings.Path)
	budgets := rego.Defaults()
	if err != nil || !info.Mode().IsRegular() || info.Size() > budgets.MaxBodyBytes {
		return nil, &failure{ErrPolicy, err}
	}
	file, err := os.Open(settings.Path)
	if err != nil {
		return nil, &failure{ErrPolicy, err}
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, budgets.MaxBodyBytes+1))
	if err != nil || int64(len(body)) > budgets.MaxBodyBytes {
		return nil, &failure{ErrPolicy, err}
	}
	budgets.Timeouts.Initialization = settings.Timeout
	budgets.Timeouts.Combined = settings.Timeout
	engine, err := rego.NewWorker(budgets)
	if err != nil {
		return nil, &failure{ErrPolicy, err}
	}
	policy := rego.Policy{Type: rego.Initialization, Engine: settings.Engine, Body: string(body)}
	if err := engine.Check(ctx, policy); err != nil {
		return nil, &failure{ErrPolicy, err}
	}
	return &Policy{engine: engine, policy: policy, timeout: settings.Timeout}, nil
}

// Input is the typed version-1 worker projection. It contains no environment,
// source credentials, pool private key or opened workload secrets.
type Input struct {
	Version    int               `json:"version"`
	Run        Run               `json:"run"`
	Static     StaticEnvironment `json:"static_run_environment"`
	Tool       *Tool             `json:"tool"`
	Terragrunt *Terragrunt       `json:"terragrunt"`
}

// Run contains the assignment's run facts; metadata is currently always empty.
// Token is the run token the launcher admitted against TokenTrust.
type Run struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Metadata   []string   `json:"user_provided_metadata"`
	Commit     Commit     `json:"commit"`
	Token      string     `json:"token"`
	TokenTrust TokenTrust `json:"token_trust"`
}

// TokenTrust is what verified the run token: the server's issuer, the run
// tokens' audience and the server's public keys as an RFC 7517 JWK Set
// document, which io.jwt.decode_verify takes as its cert.
type TokenTrust struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	JWKS     string `json:"jwks"`
}

// Commit is the run's actual source commit, not the tracked branch's head.
type Commit struct {
	Hash    string `json:"hash"`
	Branch  string `json:"branch"`
	Message string `json:"message"`
}

// StaticEnvironment names trusted ownership and the resolved runner image.
type StaticEnvironment struct {
	StackID     string `json:"stack_id"`
	StackSlug   string `json:"stack_slug"`
	Space       string `json:"space"`
	PoolID      string `json:"pool_id"`
	AccountID   string `json:"account_id"`
	RunnerImage string `json:"runner_image"`
}

// Tool preserves explicit requested version/download independently of baked tools.
type Tool struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Download bool   `json:"download"`
}

// Terragrunt is null when the run has no Terragrunt driver.
type Terragrunt struct {
	Version  string `json:"version"`
	Download bool   `json:"download"`
}

// Snapshot projects the specification authenticated by the caller's opened
// pool envelope, with the trust the run token was admitted against,
// refusing a mismatched run/state version or missing new facts.
func Snapshot(a wire.Assignment, spec wire.RunSpec, pool, image string, trust TokenTrust) (Input, error) {
	if a.Run != spec.Run.Id || a.StateVersion != spec.Run.StateVersion || string(a.State) != spec.Run.State ||
		spec.Stack.Ulid == nil || *spec.Stack.Ulid == "" || spec.Commit.Branch == nil ||
		a.RunToken == "" || spec.Run.Account == "" || pool == "" || image == "" || spec.Stack.Id == "" || spec.Stack.Space == "" {
		return Input{}, ErrInput
	}
	in := Input{
		Version: 1,
		Run: Run{
			ID: a.Run, Type: string(spec.Run.Type), Metadata: []string{}, Token: a.RunToken, TokenTrust: trust,
			Commit: Commit{Hash: spec.Commit.Hash, Branch: *spec.Commit.Branch, Message: spec.Commit.Message},
		},
		Static: StaticEnvironment{
			StackID: *spec.Stack.Ulid, StackSlug: spec.Stack.Id, Space: spec.Stack.Space,
			PoolID: pool, AccountID: spec.Run.Account, RunnerImage: image,
		},
	}
	if t := spec.Tool; t != nil {
		in.Tool = &Tool{Name: string(t.Name), Version: t.Version, Download: t.Download}
	}
	if t := spec.Terragrunt; t != nil {
		in.Terragrunt = &Terragrunt{Version: t.Version, Download: t.Download}
	}
	return in, nil
}

// Result contains only bounded, masked output from one evaluation.
type Result struct {
	Deny           []string
	Print          string
	PrintTruncated bool
}

// Denied is the safe preparation failure from a nonempty deny set.
type Denied struct{ Reasons []string }

func (d *Denied) Error() string {
	return "worker initialization policy denied: " + strings.Join(d.Reasons, "; ")
}

// Evaluate fails closed on evaluation/result errors. An empty deny set allows
// the run, following the existing initialization preamble; helpers may be undefined.
// commands runs this evaluation's exec calls; with none, a call fails the
// evaluation. A call that produces no result fails it with the call's reason.
func (p *Policy) Evaluate(ctx context.Context, input Input, commands rego.Commands) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	if err := input.validate(p.engine.InputLimit()); err != nil {
		return Result{}, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return Result{}, &failure{ErrInput, err}
	}
	defer clear(body)
	masker := mask.New([]byte(input.Run.Token)).WellKnown()
	out, err := p.engine.Evaluate(ctx, rego.Request{Policy: p.policy, Input: body, Redact: masker.String, Commands: commands})
	if err != nil {
		return Result{}, &failure{ErrEvaluation, err}
	}
	defer clear(out.Decision)
	var decision struct {
		Deny []string `json:"deny"`
	}
	if err := json.Unmarshal(out.Decision, &decision); err != nil {
		return Result{}, &failure{ErrEvaluation, err}
	}
	result := Result{Deny: decision.Deny, Print: out.Print, PrintTruncated: out.PrintTruncated}
	if len(result.Deny) != 0 {
		return result, &Denied{Reasons: result.Deny}
	}
	return result, nil
}

func (in Input) validate(limit int64) error {
	trust := in.Run.TokenTrust
	if in.Version != 1 || in.Run.ID == "" || in.Run.Token == "" || in.Run.Metadata == nil ||
		trust.Issuer == "" || trust.Audience == "" || trust.JWKS == "" ||
		in.Static.StackID == "" || in.Static.StackSlug == "" || in.Static.Space == "" ||
		in.Static.PoolID == "" || in.Static.AccountID == "" || in.Static.RunnerImage == "" {
		return ErrInput
	}
	switch in.Run.Type {
	case "PROPOSED", "TRACKED", "TASK", "TESTING", "DESTROY", "PARSE":
	default:
		return ErrInput
	}
	words := []string{
		in.Run.ID, in.Run.Token, trust.Issuer, trust.Audience, trust.JWKS, in.Run.Type, in.Run.Commit.Hash, in.Run.Commit.Branch,
		in.Run.Commit.Message, in.Static.StackID, in.Static.StackSlug, in.Static.Space,
		in.Static.PoolID, in.Static.AccountID, in.Static.RunnerImage,
	}
	if in.Tool != nil {
		words = append(words, in.Tool.Name, in.Tool.Version)
	}
	if in.Terragrunt != nil {
		words = append(words, in.Terragrunt.Version)
	}
	// Reject oversized typed facts before JSON allocates an encoding. The
	// engine separately checks the encoded size before decoding or evaluation.
	size := int64(len(in.Run.Metadata)) * 3
	for _, word := range words {
		size += int64(len(word))
	}
	for _, word := range in.Run.Metadata {
		size += int64(len(word))
	}
	if size > limit {
		return &failure{ErrInput, rego.ErrLimit}
	}
	return nil
}
