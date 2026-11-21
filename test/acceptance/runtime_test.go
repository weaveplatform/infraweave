package acceptance

import (
	"crypto/fips140"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// TestAccRuntimeCryptoMode checks the actual application against the test
// process's crypto mode and module. Keep its evidence beside coverage so CI
// can report the tested runtime after TestMain removes the executable.
func TestAccRuntimeCryptoMode(t *testing.T) {
	bin, err := harness.Binary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	dir, given := harness.CoverDir(t)
	cmd := exec.CommandContext(t.Context(), bin, "version")
	cmd.Env = append(os.Environ(), "GOCOVERDIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("application version: %v\n%s", err, out)
	}
	mode := "off"
	if fips140.Enforced() {
		mode = "only"
	} else if fips140.Enabled() {
		mode = "on"
	}
	want := fmt.Sprintf("FIPS 140-3 mode %s, Go Cryptographic Module %s", mode, fips140.Version())
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	got := strings.TrimSpace(lines[len(lines)-1])
	if got != want {
		t.Fatalf("application crypto runtime = %q, test runtime = %q", got, want)
	}
	t.Log(got)
	if given {
		if err := os.WriteFile(filepath.Join(dir, "runtime-crypto-mode.txt"), []byte(got+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
