package vcslab

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestUnitSeed pushes seed.sh's history to a bare repository on disk and
// checks that it writes the commits the constants name, so a change to
// the script that moves a hash fails here rather than in the vcs-lab
// job.
func TestUnitSeed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("seed.sh runs on the vcs-lab job's Linux runner")
	}
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s: %v", tool, err)
		}
	}
	bare := filepath.Join(t.TempDir(), "lab.git")
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "--bare", "-b", Branch, bare)
	seed := exec.CommandContext(
		t.Context(),
		"bash",
		filepath.Join("..", "..", "..", "test", "terraform", "vcs", "seed.sh"),
	)
	seed.Env = append(os.Environ(), "SEED_URL="+bare, "SEED_TOKEN=")
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("seed.sh: %v\n%s", err, out)
	}
	for ref, want := range map[string]string{
		Branch + "~2":      Base,
		Tag + "^{commit}":  Tagged,
		Branch:             Head,
		DevBranch:          Dev,
		DevBranch + "~1":   Head,
		"refs/tags/" + Tag: "",
	} {
		got := git("-C", bare, "rev-parse", ref)
		if want != "" && got != want {
			t.Errorf("%s = %s, want %s", ref, got, want)
		}
	}
	if kind := git("-C", bare, "cat-file", "-t", Tag); kind != "tag" {
		t.Errorf("%s is a %s, want an annotated tag", Tag, kind)
	}
	if got := git("-C", bare, "show", Head+":"+File); got+"\n" != Content {
		t.Errorf("%s at Head = %q", File, got)
	}
	changed := strings.Fields(git("-C", bare, "diff", "--name-only", Base, Head))
	if !slices.Equal(changed, Changed()) {
		t.Errorf("Base..Head changes %q, want %q", changed, Changed())
	}
}

// TestUnitFromEnv checks that FromEnv reads every variable and names
// each required one that is unset.
func TestUnitFromEnv(t *testing.T) {
	for v, value := range map[string]string{
		EnvHost: "https://gitlab.example", EnvUser: "", EnvToken: "tok", EnvNamespace: "lab",
		EnvRepository: "infra", EnvOther: "other", EnvCAFile: "",
	} {
		t.Setenv(v, value)
	}
	lab, err := FromEnv(EnvHost, EnvToken, EnvNamespace, EnvRepository, EnvOther)
	if err != nil || lab != (Lab{
		Host: "https://gitlab.example", Token: "tok", Namespace: "lab",
		Repository: "infra", Other: "other",
	}) {
		t.Errorf("FromEnv = %+v, %v", lab, err)
	}
	_, err = FromEnv(EnvUser, EnvToken, EnvCAFile)
	if !errors.Is(err, ErrNotConfigured) ||
		!strings.HasSuffix(err.Error(), EnvUser+", "+EnvCAFile+" unset") ||
		strings.Contains(err.Error(), "tok") {
		t.Errorf("FromEnv with two unset = %v", err)
	}
}
