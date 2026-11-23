package cli_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/bootstrap"
	"github.com/weaveplatform/infraweave/internal/agent/cli"
)

func TestUnitRunnerBootstrapReceivesItsStream(t *testing.T) {
	src, root := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "main.tf"), []byte("# main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var ws bytes.Buffer
	desc, err := bootstrap.Pack(&ws, src)
	if err != nil {
		t.Fatal(err)
	}
	paths := bootstrap.Paths{Workspace: filepath.Join(root, "ws"), Tools: filepath.Join(root, "tools"), Binary: filepath.Join(root, "bin", "infraweave")}
	for _, d := range []string{paths.Workspace, paths.Tools, filepath.Dir(paths.Binary)} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	m := bootstrap.Manifest{Version: bootstrap.Version, Run: "01KRUN", StateVersion: 2, Worker: "01KWORKER", Workspace: desc}
	var in bytes.Buffer
	if err := bootstrap.Send(&in, m, bytes.NewReader(ws.Bytes()), nil); err != nil {
		t.Fatal(err)
	}
	stream := in.Bytes()
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		cmd := cli.PodRunnerCmd(paths, nil)
		cmd.SetIn(bytes.NewReader(stream))
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(append([]string{"bootstrap"}, args...))
		err := cmd.ExecuteContext(t.Context())
		return out.String(), err
	}
	if _, err := run("--run", "01KRUN", "--state-version", "3", "--worker", "01KWORKER"); !errors.Is(err, bootstrap.ErrStream) {
		t.Fatalf("a stream of another state version: %v", err)
	}
	if _, err := run("--run", "01KRUN"); err == nil {
		t.Fatal("bootstrapped without its assignment")
	}
	out, err := run("--run", "01KRUN", "--state-version", "2", "--worker", "01KWORKER")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "received run 01KRUN at state version 2") || strings.Contains(out, "# main") {
		t.Fatalf("output %q", out)
	}
	if b, err := os.ReadFile(filepath.Join(paths.Workspace, "main.tf")); err != nil || string(b) != "# main\n" {
		t.Fatalf("workspace %q %v", b, err)
	}
	if _, err := os.Stat(paths.Binary); err != nil {
		t.Fatal(err)
	}
}

func TestUnitRunnerSignalInterruptsProcessOne(t *testing.T) {
	var got []int
	interrupt := func(pid int) error {
		got = append(got, pid)
		return nil
	}
	cmd := cli.PodRunnerCmd(bootstrap.PodPaths, interrupt)
	cmd.SetArgs([]string{"signal", "interrupt"})
	if err := cmd.ExecuteContext(t.Context()); err != nil || len(got) != 1 || got[0] != 1 {
		t.Fatalf("%v %v", got, err)
	}
	cmd = cli.PodRunnerCmd(bootstrap.PodPaths, interrupt)
	cmd.SetArgs([]string{"signal", "kill"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.ExecuteContext(t.Context()); err == nil || len(got) != 1 {
		t.Fatalf("sent another signal: %v", err)
	}
}
