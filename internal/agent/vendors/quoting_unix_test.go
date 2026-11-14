//go:build unix

package vendors_test

import (
	"bytes"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/vendors"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/iac/workflow"
)

// TestUnitWorkflowQuoting renders workspace names and targets that hold
// quotes, spaces, command substitutions, backticks, newlines and
// semicolons into a command that prints each of its arguments ended by a
// NUL byte. sh -n parses the line, and sh runs it: each value comes back
// as one argument, unchanged, and nothing in it ran.
//
// ref: decision 0024 "Workflow command templates"
func TestUnitWorkflowQuoting(t *testing.T) {
	hostile := []string{
		"x; touch pwned", "it's", `"double"`, "$(touch pwned)", "`touch pwned`",
		"a\nb", "a b\tc", "$HOME", "x&touch$IFS@pwned", `\'`, "'", "", "*", "${IFS}",
	}
	w, err := vendors.Read([]byte(file(workflow.Plan,
		`printf '%s\0' ${{ workspaceName }} ${{ targets.map(t, "-target=" + t) }}`)))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, value := range hostile {
		p := params()
		p.WorkspaceName, p.Targets = value, []string{value, "module.a"}
		got, err := w.Commands(t.Context(), wire.WorkerStatePLANNING, vendors.Options{Params: p})
		if err != nil {
			t.Fatal(err)
		}
		line := got[0].Line
		check := exec.CommandContext(t.Context(), "sh", "-n", "-c", line)
		if out, err := check.CombinedOutput(); err != nil {
			t.Errorf("%q: sh -n: %v: %s", value, err, out)
			continue
		}
		run := exec.CommandContext(t.Context(), "sh", "-c", line)
		run.Dir = dir
		var out bytes.Buffer
		run.Stdout = &out
		if err := run.Run(); err != nil {
			t.Errorf("%q: sh: %v", value, err)
			continue
		}
		args := strings.Split(strings.TrimSuffix(out.String(), "\x00"), "\x00")
		want := []string{value, "-target=" + value, "-target=module.a"}
		if !reflect.DeepEqual(args, want) {
			t.Errorf("%q rendered as %s: the shell read %q", value, line, args)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("a value ran as a command: %v, %v", entries, err)
	}
}
