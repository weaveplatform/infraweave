//go:build unix

package exec_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/exec"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/agent/workspace"
)

// oidcToken is a federation token's stand-in, long enough that masking
// and the archive's scan match it alone.
const oidcToken = "eyJhbGciOiJSUzI1NiJ9.eyJzY29wZSI6InJlYWQifQ.c2lnbmF0dXJl"

// TestUnitExecOIDCToken checks that a handoff's federation token reaches
// the phase as INFRAWEAVE_OIDC_TOKEN, after a stack variable of that name,
// and at /mnt/workspace/infraweave.oidc, read-only to the runner's user,
// before the first hook, replacing a mounted file there; that the log
// masks it; and that the saved workspace leaves its file out.
func TestUnitExecOIDCToken(t *testing.T) {
	w := newWorld(t)
	token, mine, stale := oidcToken, "mine", []byte("stale")
	w.h.Secrets.OidcToken = &token
	w.h.Spec.Environment = append(w.h.Spec.Environment, wire.RunSpecVariable{Name: "INFRAWEAVE_OIDC_TOKEN", Value: &mine})
	w.h.Spec.Files = append(w.h.Spec.Files, wire.RunSpecFile{Path: workspace.OIDCTokenFile, Content: &stale})
	w.h.Spec.Hooks = map[string][]string{
		"before_init": {
			`[ "$(cat /dev/null "$OIDC_FILE")" = "$INFRAWEAVE_OIDC_TOKEN" ] && echo "same token"`,
			`echo "token $INFRAWEAVE_OIDC_TOKEN"`,
			`echo "mode $(stat -c %a "$OIDC_FILE" 2>/dev/null || stat -f %Lp "$OIDC_FILE")"`,
			`( echo again > "$OIDC_FILE" ) 2>/dev/null || echo "read-only"`,
		},
	}
	w.environ = append(w.environ, "OIDC_FILE="+filepath.Join(w.workspace, workspace.OIDCTokenFile))
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	out := w.out.String()
	for _, want := range []string{"same token\n", "token *****\n", "mode 400\n", "read-only\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, oidcToken) {
		t.Errorf("the log holds the token: %s", out)
	}
	if b, err := os.ReadFile(filepath.Join(w.workspace, workspace.OIDCTokenFile)); err != nil || string(b) != oidcToken {
		t.Errorf("the token's file = %q, %v", b, err)
	}
	saved := w.w.recorded[exec.WorkspaceName]
	dir := filepath.Join(t.TempDir(), "restored")
	if err := workspace.Read(bytes.NewReader(saved), dir, w.pool, runID, 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, workspace.OIDCTokenFile)); !os.IsNotExist(err) {
		t.Errorf("the saved workspace holds the token's file: %v", err)
	}
}

// TestUnitExecNoOIDCToken checks that a handoff without a federation token
// gives the phase neither the variable nor the file.
func TestUnitExecNoOIDCToken(t *testing.T) {
	w := newWorld(t)
	w.h.Spec.Hooks = map[string][]string{
		"before_init": {
			`echo "variable ${INFRAWEAVE_OIDC_TOKEN-unset}"`,
			`[ -e "$OIDC_FILE" ] || echo "no file"`,
		},
	}
	w.environ = append(w.environ, "OIDC_FILE="+filepath.Join(w.workspace, workspace.OIDCTokenFile))
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if out := w.out.String(); !strings.Contains(out, "variable unset\n") || !strings.Contains(out, "no file\n") {
		t.Errorf("output: %s", out)
	}
}

// TestUnitExecOIDCTokenSaveFails checks that planning fails, naming the
// file, when a hook copies the federation token into the workspace, and
// that a token's file the runner cannot write fails the first phase.
func TestUnitExecOIDCTokenSaveFails(t *testing.T) {
	w := newWorld(t)
	token := oidcToken
	w.h.Secrets.OidcToken = &token
	w.h.Spec.Hooks = map[string][]string{
		"after_plan": {`echo "$INFRAWEAVE_OIDC_TOKEN" > leak.txt`},
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	events := w.events()
	if last := events[len(events)-1]; !strings.HasPrefix(last, "PLANNING failed 5: ") ||
		!strings.Contains(last, "holds one of the run's credentials: the file source/leak.txt") {
		t.Errorf("the last report: %s", last)
	}

	w = newWorld(t)
	w.h.Secrets.OidcToken = &token
	if err := os.Mkdir(filepath.Join(w.workspace, workspace.OIDCTokenFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.workspace, workspace.OIDCTokenFile, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	events = w.events()
	if last := events[len(events)-1]; !strings.Contains(last, "failed") ||
		!strings.Contains(last, "The OIDC federation token cannot be written: ") {
		t.Errorf("the last report: %s", last)
	}
}
