//go:build unix

package exec_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/exec"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/agent/workspace"
	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
)

// opened is the artifact name a run recorded at version, opened with the
// content key the server derives for it.
func (w *world) opened(version int64, name string) []byte {
	w.t.Helper()
	acct, err := kernel.ParseAccountID(acctID)
	if err != nil {
		w.t.Fatal(err)
	}
	run, err := kernel.ParseULID(runID)
	if err != nil {
		w.t.Fatal(err)
	}
	key, err := seal.ArtifactKey(w.h.Secrets.ArtifactKey,
		kernel.RunObjectKey(acct, run, kernel.RunStatePlanning, version, name))
	if err != nil {
		w.t.Fatal(err)
	}
	r, err := seal.NewReader(bytes.NewReader(w.w.recorded[name]), key)
	if err != nil {
		w.t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		w.t.Fatalf("open %s: %v", name, err)
	}
	return b
}

// TestUnitExecSave checks that planning saves the plan's JSON and its
// changes, which the server opens with the run's artifact key, and the
// workspace, which only the pool's key opens and which holds the plan
// file but not a link out of it, which the log names, before its
// succeeded report.
func TestUnitExecSave(t *testing.T) {
	w := newWorld(t)
	w.h.Spec.Hooks = map[string][]string{
		"after_plan": {"echo kept > note.txt", "ln -s /etc/hosts hosts"},
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.out.String(), "infraweave: the workspace's hosts is not saved: it is "+
		"a link that leaves the workspace, a device, a pipe or a socket\n") {
		t.Errorf("output: %s", w.out.String())
	}
	equal(t, "reports", w.events(), []string{
		"INITIALIZING started 3", "INITIALIZING succeeded 4", "PLANNING succeeded 5",
	})
	if got := string(w.opened(5, exec.PlanArtifact)); got != planJSON {
		t.Errorf("plan.json = %q", got)
	}
	changes, err := plan.Read(bytes.NewReader(w.opened(5, exec.ChangesArtifact)))
	if err != nil || len(changes) != 1 || changes[0].Address != "terraform_data.x" {
		t.Errorf("changes.json = %+v, %v", changes, err)
	}
	saved := w.w.recorded[exec.WorkspaceName]
	if bytes.Contains(saved, []byte("binary-plan")) {
		t.Error("the saved workspace holds the plan in plaintext")
	}
	dir := filepath.Join(t.TempDir(), "restored")
	if err := workspace.Read(bytes.NewReader(saved), dir, w.pool, runID, 1<<20); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"infraweave.plan": "binary-plan\n", "note.txt": "kept\n",
	} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != want {
			t.Errorf("%s = %q, %v", name, b, err)
		}
	}
}

// TestUnitExecSaveNoWorkspace checks that a proposed run and a plan that
// changes nothing save the plan's results and no workspace, since neither
// waits for confirmation.
func TestUnitExecSaveNoWorkspace(t *testing.T) {
	for name, edit := range map[string]func(w *world){
		"a proposed run": func(w *world) { w.h.Spec.Run.Type = wire.PROPOSED },
		"no changes": func(w *world) {
			if err := os.WriteFile(w.plan, []byte(`{"format_version":"1.2"}`), 0o600); err != nil {
				w.t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			edit(w)
			if err := w.run(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(w.w.taken, "\n"),
				"artifacts 5: artifact plan.json, artifact changes.json\n") {
				t.Errorf("taken: %q", w.w.taken)
			}
			if strings.Contains(w.out.String(), "saved the workspace") {
				t.Errorf("output: %s", w.out.String())
			}
		})
	}
}

// TestUnitExecSaveFails checks that planning fails, with a note that says
// why, when its results cannot be saved: a workspace file that holds the
// run token, a pool key that is no P-256 point, a specification with no
// account, and uploads the worker API refuses.
func TestUnitExecSaveFails(t *testing.T) {
	for name, c := range map[string]struct {
		edit func(w *world)
		want string
	}{
		"the run token in a file": {
			edit: func(w *world) {
				w.h.Spec.StateBackend = &wire.RunSpecStateBackend{Path: "/state/v1/stacks/infra"}
				w.h.Spec.Hooks = map[string][]string{
					"after_plan": {`mkdir -p out && echo "$TF_HTTP_PASSWORD" > out/leak.txt`},
				}
			},
			want: "The workspace could not be saved for the plan's confirmation: archive the " +
				"workspace: a file of the workspace holds one of the run's credentials: the file " +
				"out/leak.txt.",
		},
		"the source token in a link": {
			edit: func(w *world) {
				w.h.Secrets.SourceCredential = &wire.RunSpecSourceCredential{
					Username: "x", Token: "ghs_" + strings.Repeat("s", 36),
				}
				w.h.Spec.Hooks = map[string][]string{
					"after_plan": {"ln -s ghs_" + strings.Repeat("s", 36) + " l"},
				}
			},
			want: "the link l.",
		},
		"a pool key that is no point": {
			edit: func(w *world) { w.h.PoolKey = []byte{4, 1, 2} },
			want: "The workspace could not be saved for the plan's confirmation: the pool's key: ",
		},
		"no account": {
			edit: func(w *world) { w.h.Spec.Run.Account = "" },
			want: "The run's specification names no account: ",
		},
		"a refused upload": {
			edit: func(w *world) { w.w.uploads = &client.Error{Op: "upload-urls", Status: 403} },
			want: "The plan's results could not be uploaded: ",
		},
		"other uploads": {
			edit: func(w *world) { w.w.rename = true },
			want: "The plan's results could not be uploaded: the worker API answered other " +
				"uploads than declared.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			c.edit(w)
			if err := w.run(); err != nil {
				t.Fatal(err)
			}
			events := w.events()
			if last := events[len(events)-1]; !strings.HasPrefix(last, "PLANNING failed 5: ") ||
				!strings.Contains(last, c.want) {
				t.Errorf("the last report: %s, want %s", last, c.want)
			}
			if strings.Contains(strings.Join(events, "\n"), "ghs_ssss") {
				t.Errorf("a report names the token: %q", events)
			}
		})
	}
}
