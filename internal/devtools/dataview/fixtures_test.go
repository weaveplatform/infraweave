package dataview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func documentFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "implementation", "testdata", "dataview", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}
