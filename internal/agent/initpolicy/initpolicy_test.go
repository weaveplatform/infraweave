package initpolicy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/initcommand"
	"github.com/weaveplatform/infraweave/internal/agent/initpolicy"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

func settings(t *testing.T, dialect rego.EngineType, body string) initpolicy.Settings {
	t.Helper()
	file := filepath.Join(t.TempDir(), "initialization.rego")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return initpolicy.Settings{Path: file, Engine: dialect, Timeout: initpolicy.DefaultTimeout}
}

func input() initpolicy.Input {
	return initpolicy.Input{
		Version: 1,
		Run: initpolicy.Run{
			ID: "run", Type: "TRACKED", Token: "signed-run-secret", Metadata: []string{},
			Commit: initpolicy.Commit{Hash: "abc", Branch: "feature/source", Message: "Change"},
		},
		Static: initpolicy.StaticEnvironment{StackID: "immutable", StackSlug: "stack", Space: "legacy", PoolID: "pool", AccountID: "account", RunnerImage: "image@sha256:digest"},
	}
}

func TestUnitInitializationPreambleAndDialects(t *testing.T) {
	for _, dialect := range []rego.EngineType{rego.V0, rego.V1} {
		body := `package infraweave
import future.keywords
allowed if { input.version == 1; input.run.type == "TRACKED" }
branches := [b | some b in [input.run.commit.branch]; startswith(b, "feature/")]
deny contains "unexpected snapshot" if { not allowed }
deny contains "missing branch" if { count(branches) != 1 }
deny contains "undefined helper" if { input.missing == "deny" }
`
		p, err := initpolicy.Load(t.Context(), settings(t, dialect, body))
		if err != nil {
			t.Fatal(err)
		}
		in := input()
		out, err := p.Evaluate(t.Context(), in, nil)
		if err != nil || len(out.Deny) != 0 {
			t.Fatalf("%s allow: %+v %v", dialect, out, err)
		}
		in.Run.Type = "TASK"
		out, err = p.Evaluate(t.Context(), in, nil)
		var denied *initpolicy.Denied
		if !errors.As(err, &denied) || len(out.Deny) != 1 || out.Deny[0] != "unexpected snapshot" {
			t.Fatalf("deny: %+v %v", out, err)
		}
	}
}

func TestUnitInitializationStartupFailures(t *testing.T) {
	for name, body := range map[string]string{
		"parse":     "package infraweave\ndeny contains",
		"type":      "package infraweave\ndeny contains \"bad\" if { plus(1, true) == 2 }",
		"exec type": "package infraweave\ndeny contains \"bad\" if { exec(\"touch should-not-run\").status == 0 }",
		"http":      "package infraweave\ndeny contains \"bad\" if { http.send({\"url\":\"http://localhost\"}).status_code == 200 }",
		"package":   "package other",
		"oversized": strings.Repeat("x", (256<<10)+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := initpolicy.Load(t.Context(), settings(t, rego.V1, body)); !errors.Is(err, initpolicy.ErrPolicy) || strings.Contains(err.Error(), body) {
				t.Fatalf("startup: %v", err)
			}
		})
	}
	s := settings(t, rego.V1, "package infraweave")
	if err := os.Remove(s.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := initpolicy.Load(t.Context(), s); !errors.Is(err, initpolicy.ErrPolicy) {
		t.Fatal(err)
	}
	s.Path = t.TempDir()
	if _, err := initpolicy.Load(t.Context(), s); !errors.Is(err, initpolicy.ErrPolicy) {
		t.Fatal(err)
	}
	s.Path = ""
	if p, err := initpolicy.Load(t.Context(), s); err != nil || p != nil {
		t.Fatalf("disabled: %v %v", p, err)
	}
	for _, s := range []initpolicy.Settings{{Engine: "REGO_V2", Timeout: time.Second}, {Engine: rego.V1}, {Engine: rego.V1, Timeout: 61 * time.Second}, {Path: " ", Engine: rego.V1, Timeout: time.Second}} {
		if _, err := initpolicy.Load(t.Context(), s); !errors.Is(err, initpolicy.ErrSettings) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := initpolicy.Load(ctx, settings(t, rego.V1, "package infraweave")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestUnitInitializationMasksBeforeBoundsAndRetainsNoInput(t *testing.T) {
	p, err := initpolicy.Load(t.Context(), settings(t, rego.V1, `package infraweave
deny contains input.run.token if { print(input.run.token) }
`))
	if err != nil {
		t.Fatal(err)
	}
	in := input()
	in.Run.Token = strings.Repeat("sensitive-prefix-", 600)
	out, err := p.Evaluate(t.Context(), in, nil)
	if err == nil || len(out.Deny) != 1 || out.Deny[0] != "*****" || strings.Contains(out.Print, "sensitive-prefix-") || strings.Contains(err.Error(), "sensitive-prefix-") {
		t.Fatalf("unsafe output: %+v %v", out, err)
	}
	in.Run.Token = "second-secret-token"
	out, err = p.Evaluate(t.Context(), in, nil)
	if err == nil || out.Deny[0] != "*****" || strings.Contains(out.Print, in.Run.Token) {
		t.Fatalf("cached query retained input: %+v %v", out, err)
	}
}

func TestUnitInitializationResultAndInputBudgets(t *testing.T) {
	p, err := initpolicy.Load(t.Context(), settings(t, rego.V1, `package infraweave
deny contains 42 if true`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Evaluate(t.Context(), input(), nil); !errors.Is(err, initpolicy.ErrEvaluation) || !errors.Is(err, rego.ErrResult) {
		t.Fatalf("invalid result: %v", err)
	}
	p, err = initpolicy.Load(t.Context(), settings(t, rego.V1, `package infraweave
deny contains message if { some i in numbers.range(0, 104); message := sprintf("%d:%s", [i, concat("", ["long" | some n in numbers.range(1, 2000)])]) }
flag contains "print" if { print(concat("", ["output" | some n in numbers.range(1, 20000)])) }`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Evaluate(t.Context(), input(), nil)
	if err == nil || len(out.Deny) != rego.MessageCount || len(out.Print) > rego.PrintBytes || !out.PrintTruncated {
		t.Fatalf("bounds: %d %d %v %v", len(out.Deny), len(out.Print), out.PrintTruncated, err)
	}
	for _, message := range out.Deny {
		if len(message) > rego.MessageBytes {
			t.Fatal("unbounded message")
		}
	}
	in := input()
	in.Run.Commit.Message = strings.Repeat("x", (64<<20)+1)
	if _, err := p.Evaluate(t.Context(), in, nil); !errors.Is(err, rego.ErrLimit) {
		t.Fatal(err)
	}
	in = input()
	in.Run.Type = "INVALID"
	if _, err := p.Evaluate(t.Context(), in, nil); !errors.Is(err, initpolicy.ErrInput) {
		t.Fatal(err)
	}
	in = input()
	in.Run.Metadata = nil
	if _, err := p.Evaluate(t.Context(), in, nil); !errors.Is(err, initpolicy.ErrInput) {
		t.Fatal(err)
	}
}

func TestUnitInitializationCancellationAndCombinedDeadline(t *testing.T) {
	s := settings(t, rego.V1, `package infraweave
deny contains "late" if {
 some a in numbers.range(0, 1000)
 some b in numbers.range(0, 1000)
 some c in numbers.range(0, 1000)
 a+b+c == -1
}`)
	s.Timeout = 500 * time.Millisecond
	p, err := initpolicy.Load(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Evaluate(ctx, input(), nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := p.Evaluate(ctx, input(), nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("caller deadline was extended")
	}
	started = time.Now()
	if _, err := p.Evaluate(t.Context(), input(), nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("combined worker budget was extended")
	}
}

func TestUnitInitializationSnapshot(t *testing.T) {
	stackID, branch := "stack-ulid", "feature/source"
	a := wire.Assignment{Run: "run", State: wire.AssignmentStatePREPARINGAPPLY, StateVersion: 4, RunToken: "signed-token"}
	spec := wire.RunSpec{
		Run:        wire.RunSpecRun{Id: "run", Type: wire.RunSpecRunType("TRACKED"), State: "PREPARING_APPLY", StateVersion: 4, Account: "account"},
		Stack:      wire.RunSpecStack{Id: "slug", Ulid: &stackID, Space: "legacy", Branch: "tracked-branch"},
		Commit:     wire.RunSpecCommit{Hash: "abc", Branch: &branch, Message: "message"},
		Tool:       &wire.RunSpecTool{Name: wire.RunSpecToolName("terraform"), Version: "1.17.2", Download: true},
		Terragrunt: &wire.RunSpecTerragrunt{Version: "1.2.2", Download: false},
	}
	in, err := initpolicy.Snapshot(a, spec, "pool", "resolved-image")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(in)
	want := `{"version":1,"run":{"id":"run","type":"TRACKED","user_provided_metadata":[],"commit":{"hash":"abc","branch":"feature/source","message":"message"},"token":"signed-token"},"static_run_environment":{"stack_id":"stack-ulid","stack_slug":"slug","space":"legacy","pool_id":"pool","account_id":"account","runner_image":"resolved-image"},"tool":{"name":"terraform","version":"1.17.2","download":true},"terragrunt":{"version":"1.2.2","download":false}}`
	if string(body) != want {
		t.Fatalf("snapshot: %s", body)
	}
	spec.Tool, spec.Terragrunt = nil, nil
	in, err = initpolicy.Snapshot(a, spec, "pool", "image")
	if err != nil || in.Tool != nil || in.Terragrunt != nil {
		t.Fatal("absent tools fabricated")
	}
	for _, mutate := range []func(*wire.RunSpec){func(s *wire.RunSpec) { s.Run.Id = "other" }, func(s *wire.RunSpec) { s.Run.StateVersion++ }, func(s *wire.RunSpec) { s.Stack.Ulid = nil }, func(s *wire.RunSpec) { s.Commit.Branch = nil }} {
		bad := spec
		mutate(&bad)
		if _, err := initpolicy.Snapshot(a, bad, "pool", "image"); !errors.Is(err, initpolicy.ErrInput) {
			t.Fatal(err)
		}
	}
}

// commander serves each helper in-process with a fixed result, as a
// container would after running its command.
type commander struct {
	argv   [][]string
	status int
	stdout string
	err    error
}

func (c *commander) Command(_ context.Context, cmd executor.Command) error {
	if c.err != nil {
		return c.err
	}
	req, err := initcommand.ReadRequest(bytes.NewReader(cmd.Request))
	if err != nil {
		return err
	}
	c.argv = append(c.argv, req.Argv)
	if err := initcommand.WriteOutput(cmd.Output, false, []byte(c.stdout)); err != nil {
		return err
	}
	return initcommand.WriteResult(cmd.Output, initcommand.Result{Call: req.Call, Status: initcommand.StatusExited, ExitCode: c.status})
}

func TestUnitInitializationExecReachesTheVerifierAndFailsClosed(t *testing.T) {
	body := `package infraweave
import future.keywords
verified := exec(["verify", "--token", input.run.token])
deny contains "unverified" if { verified.status != 0 }
deny contains verified.stdout if { verified.stdout != "ok" }
`
	p, err := initpolicy.Load(t.Context(), settings(t, rego.V1, body))
	if err != nil {
		t.Fatal(err)
	}
	session := func(c *commander) *initcommand.Session {
		return initcommand.NewSession(c, executor.Command{Worker: "w", Run: "run", Image: "image@sha256:digest"})
	}
	ok := &commander{stdout: "ok"}
	if out, err := p.Evaluate(t.Context(), input(), session(ok)); err != nil || len(out.Deny) != 0 {
		t.Fatalf("a verified run: %+v, %v", out, err)
	}
	if len(ok.argv) != 1 || strings.Join(ok.argv[0], " ") != "verify --token signed-run-secret" {
		t.Fatalf("argv = %q", ok.argv)
	}
	_, err = p.Evaluate(t.Context(), input(), session(&commander{status: 1, stdout: "signed-run-secret"}))
	var denied *initpolicy.Denied
	if !errors.As(err, &denied) || err.Error() != "worker initialization policy denied: *****; unverified" {
		t.Fatalf("an unverified run: %v", err)
	}
	_, err = p.Evaluate(t.Context(), input(), session(&commander{err: fmt.Errorf("pull image@sha256:digest: %w", executor.ErrImage)}))
	if !errors.Is(err, initpolicy.ErrEvaluation) || !errors.Is(err, initcommand.ErrImage) ||
		err.Error() != "worker initialization policy evaluation failed: the initialization command's runner image is unavailable" {
		t.Fatalf("an unavailable image: %v", err)
	}
	_, err = p.Evaluate(t.Context(), input(), nil)
	if !errors.Is(err, rego.ErrNoCommands) || err.Error() != "worker initialization policy evaluation failed: initialization commands are unavailable" {
		t.Fatalf("no commands: %v", err)
	}
}
