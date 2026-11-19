package vcslab

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnitAppSetupFileIsNewPrivateAndNeverPrinted(t *testing.T) {
	one, _ := appKeyFixture(t)
	key, err := DeriveAppKey("42", one)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "secret")
	var output bytes.Buffer
	if err := WriteAppSetup(path, "42", one, &output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != key.Secret()+"\n" {
		t.Fatal("private setup file does not contain the derived key")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("setup file is not private")
	}
	if strings.Contains(output.String(), key.Secret()) || strings.Contains(output.String(), string(one)) || !strings.Contains(output.String(), key.Readiness) {
		t.Fatal("setup output is not public metadata only")
	}
	if err := WriteAppSetup(path, "42", one, &output); !errors.Is(err, ErrHook) {
		t.Fatal("setup replaced an existing file")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteAppSetup(link, "42", one, &output); !errors.Is(err, ErrHook) {
		t.Fatal("setup followed an existing symlink")
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, unchanged) {
		t.Fatal("refused setup changed the original file")
	}
}
