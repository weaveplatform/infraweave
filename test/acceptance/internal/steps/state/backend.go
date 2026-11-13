package state

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/agent/backend"
	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// backendSteps are the scenario as its runs' worker, and the tools it
// runs for them.
//
// ref: decision 0010 § "How a run reaches it"
// ref: decision 0016
type backendSteps struct {
	w *harness.World
	// host, relay, label and network are the scenario's Docker, its relay,
	// the label of the containers its end removes, and the network its
	// containers reach the relay on; the first takeover starts them.
	host    *harness.Launchers
	relay   *harness.Relay
	label   string
	network string
	runs    map[string]*takenRun
	// procs are the processes started in the background, by name.
	procs map[string]*process
	// outputs are each tool process's output, in the order they ended,
	// which a failed scenario leaves as evidence.
	outputs []string
	// next is the last report's answer.
	next wire.NextStep
}

// takenRun is a run the scenario took over: its ID, the name of its run
// token, the state version its next report names, its workspace, its
// tool's environment, its backend's path and its tool's last standard
// output.
type takenRun struct {
	id, token string
	version   int64
	dir       string
	env       []string
	path      string
	stdout    string
}

// process is a tool's process started in the background, of the run it
// works on.
type process struct {
	tool *harness.Tool
	run  *takenRun
	what string
}

// toolEnv is what every tool process runs with beyond the backend's
// credentials: no prompt, no update check, output for a machine, and a
// home directory the image has, since the host's user the container runs
// as has none there.
var toolEnv = []string{"TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1", "HOME=/tmp"}

// tools are the pinned releases of the tools a step names, and the tag a
// scenario that runs one carries.
var tools = map[string]struct {
	tool deps.Tool
	tag  string
}{
	"terraform": {deps.Terraform, "@terraform"},
	"tofu":      {deps.OpenTofu, "@tofu"},
}

func (b *backendSteps) register(sc *godog.ScenarioContext) {
	b.runs, b.procs = map[string]*takenRun{}, map[string]*process{}
	sc.After(b.end)
	sc.Step(
		`^the worker takes over the run "(\w+)" with token "(\w+)" and the key of "(\w+)", checking out "(\w+)", through ([AB])$`,
		b.takeOver,
	)
	sc.Step(
		`^the worker reports the run "(\w+)" (started|succeeded|failed) in ([A-Z_]+)(?: with the delta '([^']*)')? through ([AB])$`,
		b.report,
	)
	sc.Step(`^the report's answer is to (execute|pause|exit) in ([A-Z_]+)$`, b.answer)
	sc.Step(`^(terraform|tofu) "([^"]+)" of the run "(\w+)" exits (\d+)$`, b.exits)
	sc.Step(`^(terraform|tofu) "([^"]+)" of the run "(\w+)" starts as "(\w+)"$`, b.starts)
	sc.Step(`^the process "(\w+)" exits (\d+)$`, b.processExits)
	sc.Step(`^the last output of the run "(\w+)" is "([^"]*)"$`, b.output)
	b.registerChecks(sc)
}

// run is the run the scenario took over as name.
func (b *backendSteps) run(name string) (*takenRun, error) {
	tr, ok := b.runs[name]
	if !ok {
		return nil, fmt.Errorf("%w: the worker took over no run %q", harness.ErrStep, name)
	}
	return tr, nil
}

// start starts the scenario's relay to replica, and the network its
// containers reach it on, on first use.
func (b *backendSteps) start(ctx context.Context, replica string) error {
	if b.relay != nil {
		return nil
	}
	host, err := b.w.Suite.Launchers(ctx)
	if err != nil {
		return err
	}
	srv := b.w.Servers[slices.Index([]string{"A", "B"}, replica)]
	if b.relay, err = harness.StartRelay(ctx, srv.API); err != nil {
		return err
	}
	b.host, b.label = host, kernel.NewULID().String()
	b.network, err = host.Network(ctx, b.label, b.relay.Port)
	return err
}

// takeOver does what a launcher does with the run kept as run, whose run
// token token names, before its runner starts: it reads the run's
// specification through replica, opens its sealed part with the private
// key of the request kept as csr, checks out the run's commit of the
// repository kept as repo into a workspace of the run's own, and writes
// the backend file there with the backend package, addressed to the
// relay. Its tool runs with the credentials the package answers.
func (b *backendSteps) takeOver(ctx context.Context, run, token, csr, repo, replica string) error {
	if err := b.start(ctx, replica); err != nil {
		return err
	}
	id := b.w.IDs[run]
	r, err := b.w.Do(ctx, replica, token, http.MethodGet, client.BasePath+"/runs/"+id+"/spec", nil)
	if err != nil && r.Status == 0 {
		return err
	}
	var spec wire.RunSpec
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &spec) != nil {
		return fmt.Errorf("%w: the specification answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	if spec.StateBackend == nil {
		return fmt.Errorf("%w: the specification names no state backend", harness.ErrStep)
	}
	secrets, err := b.open(csr, spec)
	if err != nil {
		return err
	}
	dir := filepath.Join(b.w.Dir, "workspace-"+run)
	if err := b.checkOut(ctx, repo, spec.Commit.Hash, dir); err != nil {
		return err
	}
	env, err := backend.Configure(dir, handoff.Handoff{
		Endpoint: b.relay.URL + client.BasePath, Run: id, RunToken: b.w.Tokens[token],
		Assigned: wire.AssignmentState(spec.Run.State), StateVersion: spec.Run.StateVersion,
		Spec: spec, Secrets: secrets,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	b.runs[run] = &takenRun{
		id: id, token: token, version: spec.Run.StateVersion, dir: dir,
		env: append(env, toolEnv...), path: spec.StateBackend.Path,
	}
	return nil
}

// open opens spec's sealed part with the private key of the request kept
// as csr, for the run and state version spec names.
func (b *backendSteps) open(csr string, spec wire.RunSpec) (wire.RunSpecSecrets, error) {
	var secrets wire.RunSpecSecrets
	key, ok := b.w.Keys[csr].(*ecdsa.PrivateKey)
	if !ok {
		return secrets, fmt.Errorf("%w: no P-256 key of %q", harness.ErrStep, csr)
	}
	priv, err := key.ECDH()
	if err != nil {
		return secrets, fmt.Errorf("the key of %q: %w", csr, err)
	}
	plain, err := seal.OpenEnvelope(priv, spec.Run.Id, spec.Run.StateVersion, spec.Sealed)
	if err != nil {
		return secrets, fmt.Errorf("%w: the sealed part does not open: %w", harness.ErrStep, err)
	}
	if err := json.Unmarshal(plain, &secrets); err != nil {
		return secrets, fmt.Errorf("%w: the secrets: %w", harness.ErrStep, err)
	}
	return secrets, nil
}

// checkOut writes the files at commit of the repository kept as repo
// into dir.
func (b *backendSteps) checkOut(ctx context.Context, repo, commit, dir string) error {
	gitea, err := b.w.Suite.Deps.Gitea(ctx)
	if err != nil {
		return err //nolint:wrapcheck // names @vcs
	}
	name := gitea.Repository(b.w.IDs[repo+".url"])
	if name == "" {
		return fmt.Errorf("%w: no repository %q in this scenario", harness.ErrStep, repo)
	}
	files, err := gitea.Files(ctx, name, commit)
	if err != nil {
		return err //nolint:wrapcheck // names the repository
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o600); err != nil {
			return fmt.Errorf("workspace: %w", err)
		}
	}
	return nil
}

// report sends a report of event in state for the run kept as run through
// replica, naming the state version the last answer gave and, when delta
// is set, the plan's delta, and keeps its answer.
func (b *backendSteps) report(ctx context.Context, run, event, state, delta, replica string) error {
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	body := wire.RunReport{
		Event: wire.RunReportEvent(event), State: wire.WorkerState(state),
		StateVersion: tr.version,
	}
	if delta != "" {
		body.Delta = &wire.PlanDelta{}
		if err := json.Unmarshal([]byte(delta), body.Delta); err != nil {
			return fmt.Errorf("%w: the delta %s: %w", harness.ErrStep, delta, err)
		}
	}
	r, err := b.w.Do(ctx, replica, tr.token, http.MethodPost,
		client.BasePath+"/runs/"+tr.id+"/reports", body)
	if err != nil && r.Status == 0 {
		return err
	}
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &b.next) != nil {
		return fmt.Errorf("%w: the report answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	tr.version = b.next.StateVersion
	return nil
}

// answer checks the last report's answer: what the worker does next, in
// the state the run is in.
func (b *backendSteps) answer(action, state string) error {
	if string(b.next.Action) != action || b.next.State != state {
		return fmt.Errorf("%w: the answer is to %s in %s", harness.ErrStep, b.next.Action,
			b.next.State)
	}
	return nil
}

// tool starts name with the arguments command holds for the run kept as
// run, in its workspace with its environment.
func (b *backendSteps) tool(ctx context.Context, name, command, run string) (*process, error) {
	pinned := tools[name]
	if !slices.Contains(b.w.Tags, pinned.tag) {
		return nil, fmt.Errorf("%w: a scenario that runs %s needs %s", harness.ErrStep, name,
			pinned.tag)
	}
	tr, err := b.run(run)
	if err != nil {
		return nil, err
	}
	bin, err := b.w.Suite.Deps.Tool(ctx, pinned.tool, b.host.Arch)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the tool
	}
	t, err := b.host.RunTool(ctx, harness.ToolRun{
		Scenario: b.label, Network: b.network, Bin: bin, Dir: tr.dir, Env: tr.env,
		Args: strings.Fields(command),
	})
	if err != nil {
		return nil, err
	}
	return &process{tool: t, run: tr, what: name + " " + command}, nil
}

// exits runs name's command for the run kept as run and checks its exit
// code.
func (b *backendSteps) exits(ctx context.Context, name, command, run string, code int) error {
	p, err := b.tool(ctx, name, command, run)
	if err != nil {
		return err
	}
	return b.wait(ctx, p, code)
}

// starts starts name's command for the run kept as run in the background,
// as the process name.
func (b *backendSteps) starts(ctx context.Context, name, command, run, as string) error {
	p, err := b.tool(ctx, name, command, run)
	if err != nil {
		return err
	}
	b.procs[as] = p
	return nil
}

// processExits waits for the process name to exit and checks its exit
// code.
func (b *backendSteps) processExits(ctx context.Context, name string, code int) error {
	p, ok := b.procs[name]
	if !ok {
		return fmt.Errorf("%w: no process %q started", harness.ErrStep, name)
	}
	return b.wait(ctx, p, code)
}

// wait waits up to deps.WaitLimit for p to exit, keeps its standard output
// as its run's last output, and checks its exit code against want.
func (b *backendSteps) wait(ctx context.Context, p *process, want int) error {
	ctx, cancel := context.WithTimeout(ctx, deps.WaitLimit)
	defer cancel()
	code, stdout, stderr, err := p.tool.Wait(ctx)
	if err != nil {
		return err
	}
	p.run.stdout = stdout
	b.outputs = append(b.outputs, fmt.Sprintf("$ %s\nexit %d\n%s\n%s", p.what, code, stdout,
		stderr))
	if code != want {
		return fmt.Errorf("%w: %s exited %d, want %d: %s%s", harness.ErrStep, p.what, code, want,
			stdout, stderr)
	}
	return nil
}

// output checks the last standard output of the run kept as run's tool,
// up to its last newline.
func (b *backendSteps) output(run, want string) error {
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	if got := strings.TrimSuffix(tr.stdout, "\n"); got != want {
		return fmt.Errorf("%w: the output is %q", harness.ErrStep, got)
	}
	return nil
}

// end removes the scenario's containers, leaving a failed scenario's
// relay calls and tool output beside its servers' evidence, and closes
// the relay, which releases what it holds.
func (b *backendSteps) end(ctx context.Context, _ *godog.Scenario, failed error) (context.Context,
	error,
) {
	ctx = context.WithoutCancel(ctx)
	if b.relay == nil {
		return ctx, nil
	}
	var err error
	if failed != nil {
		dir := filepath.Join(b.w.Suite.Out, harness.Slug(b.w.Name))
		calls, _ := json.MarshalIndent(map[string]any{
			"calls": b.relay.Calls(), "refusals": b.relay.Refusals(),
		}, "", "  ")
		err = errors.Join(os.MkdirAll(dir, 0o750),
			os.WriteFile(filepath.Join(dir, "relay.json"), calls, 0o600),
			os.WriteFile(filepath.Join(dir, "tools.log"),
				[]byte(strings.Join(b.outputs, "\n")), 0o600))
	}
	err = errors.Join(err, b.host.Remove(ctx, harness.LabelScenario, b.label))
	return ctx, errors.Join(err, b.relay.Close(ctx))
}
