package specref

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUnitProductNamed(t *testing.T) {
	for _, p := range Products() {
		got, err := ProductNamed(p.Name)
		if err != nil || got.Name != p.Name || got.Spec != p.Spec {
			t.Errorf("ProductNamed(%q) = %+v, %v", p.Name, got, err)
		}
	}
	if _, err := ProductNamed("nope"); !errors.Is(err, ErrUnknownProduct) {
		t.Errorf("unknown product: %v", err)
	}
}

// TestUnitProductGenerate checks that Generate builds what GenerateDir
// writes, without writing it.
func TestUnitProductGenerate(t *testing.T) {
	dir := writeFixtureDir(t)
	g, err := Deploy.Generate(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, Deploy.Spec)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Generate wrote %s: %v", Deploy.Spec, err)
	}
	if g.Summary != g.Report.Summary() || g.Summary == "" {
		t.Errorf("summary %q, report says %q", g.Summary, g.Report.Summary())
	}
}

func TestUnitProductPins(t *testing.T) {
	dir := t.TempDir()
	if pins, err := Deploy.Pins(filepath.Join(dir, "missing")); err != nil || pins != nil {
		t.Errorf("no directory: %v, %v", pins, err)
	}
	if pins, err := Deploy.Pins(dir); err != nil || pins != nil {
		t.Errorf("no lock: %v, %v", pins, err)
	}
	lock := "introspection:\n  endpoint: https://example.test/graphql\n" +
		"bruno:\n  repository: o/bruno\n  ref: abc\n" +
		"provider:\n  repository: o/provider\n  ref: v1.2.3\n"
	if err := os.WriteFile(filepath.Join(dir, FileLock), []byte(lock), filePerm); err != nil {
		t.Fatal(err)
	}
	pins, err := Deploy.Pins(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"endpoint":         "https://example.test/graphql",
		"bruno-repo":       "o/bruno",
		"bruno-ref":        "abc",
		"provider-repo":    "o/provider",
		"provider-version": "1.2.3",
	}
	if !reflect.DeepEqual(pins, want) {
		t.Errorf("pins %v, want %v", pins, want)
	}
	if err := os.WriteFile(filepath.Join(dir, FileLock), []byte("bruno: [\n"), filePerm); err != nil {
		t.Fatal(err)
	}
	if _, err := Deploy.Pins(dir); err == nil {
		t.Error("malformed lock: want error")
	}
	if err := os.Remove(filepath.Join(dir, FileLock)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, FileLock), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Deploy.Pins(dir); err == nil {
		t.Error("unreadable lock: want error")
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, filePerm); err != nil {
		t.Fatal(err)
	}
	if _, err := Deploy.Pins(file); err == nil {
		t.Error("lock directory is a file: want error")
	}
}

func TestUnitProductGenerateErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := Deploy.GenerateDir(context.Background(), missing); err == nil {
		t.Error("GenerateDir of a missing directory: want error")
	}
}
