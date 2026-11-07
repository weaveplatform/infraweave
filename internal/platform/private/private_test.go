package private

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnitRestrict(t *testing.T) {
	file := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(
		file,
		[]byte("s"),
		0o644,
	); err != nil { //nolint:gosec // the file Restrict narrows
		t.Fatal(err)
	}
	if err := Check(file); !errors.Is(err, ErrShared) {
		t.Errorf("Check of a file others may read = %v, want ErrShared", err)
	}
	if err := Restrict(file); err != nil {
		t.Fatal(err)
	}
	if err := Check(file); err != nil {
		t.Errorf("Check after Restrict = %v", err)
	}
	missing := filepath.Join(t.TempDir(), "none")
	if err := Restrict(missing); err == nil {
		t.Error("restricted a file that does not exist")
	}
	if err := Check(missing); err == nil || errors.Is(err, ErrShared) {
		t.Errorf("Check of a file that does not exist = %v", err)
	}
}
