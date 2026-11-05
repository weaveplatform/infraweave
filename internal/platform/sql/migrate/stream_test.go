package migrate

import (
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func mapFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

// unreadable lists its files but fails to open them. It wraps rather than
// embeds the MapFS, whose ReadFile would otherwise bypass Open.
type unreadable struct{ m fstest.MapFS }

func (u unreadable) Open(name string) (fs.File, error) {
	if name == "." {
		return u.m.Open(name) //nolint:wrapcheck // a test file system
	}
	return nil, fs.ErrPermission
}

func TestUnitStreamLoad(t *testing.T) {
	good := Stream{Name: "stacks", Files: mapFS(map[string]string{
		"00001_create_stacks.sql": "CREATE TABLE stacks (id uuid);\n",
		"00002_index_stacks.sql": "-- infraweave:no-transaction\n-- why: a big table\n" +
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS i ON stacks (id);\n",
		"00003_drop_old.sql": "-- the contract step\n-- infraweave:compatible-from 00003\nSELECT 1;\n",
	}), Floor: Floor{Version: 2, Release: "v1.9.0"}}
	files, err := good.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[0].Name != "create_stacks" || files[1].Version != 2 {
		t.Fatalf("files = %+v", files)
	}
	if files[0].NoTransaction || !files[1].NoTransaction || files[2].CompatibleFrom != 3 {
		t.Errorf("headers: %+v", files)
	}
	if len(files[0].Checksum) != 32 {
		t.Errorf("checksum %x", files[0].Checksum)
	}
	if files, err := (Stream{Name: "empty", Files: fstest.MapFS{}}).Load(); err != nil ||
		len(files) != 0 {
		t.Errorf("empty stream: %v %v", files, err)
	}

	for _, tc := range []struct {
		name string
		s    Stream
		want string
	}{
		{"bad stream name", Stream{Name: "Stacks", Files: fstest.MapFS{}}, "lower snake case"},
		{"unreadable directory", Stream{Name: "s", Files: fstest.MapFS{"x": {Mode: fs.ModeDir}, "x/y": {}}}, "x is not"},
		{"bad file name", Stream{Name: "s", Files: mapFS(map[string]string{"1_init.sql": ""})}, "1_init.sql is not"},
		{"capitals", Stream{Name: "s", Files: mapFS(map[string]string{"00001_Init.sql": ""})}, "is not"},
		{"gap", Stream{Name: "s", Files: mapFS(map[string]string{
			"00001_a.sql": "", "00003_c.sql": "",
		})}, "00003_c.sql where version 00002 belongs"},
		{"duplicate number", Stream{Name: "s", Files: mapFS(map[string]string{
			"00001_a.sql": "", "00001_b.sql": "",
		})}, "00001_b.sql where version 00002 belongs"},
		{"not from one", Stream{Name: "s", Files: mapFS(map[string]string{"00002_a.sql": ""})}, "version 00001 belongs"},
		{"late no-transaction", Stream{Name: "s", Files: mapFS(map[string]string{
			"00001_a.sql": "-- note\n-- infraweave:no-transaction\n",
		})}, "must be the first line"},
		{"misspelt directive", Stream{Name: "s", Files: mapFS(map[string]string{
			"00001_a.sql": "-- infraweave:no-transactions\n",
		})}, "unknown directive"},
		{"future compatible-from", Stream{Name: "s", Files: mapFS(map[string]string{
			"00001_a.sql": "-- infraweave:compatible-from 00002\n",
		})}, "not a version up to 00001"},
		{"zero compatible-from", Stream{Name: "s", Files: mapFS(map[string]string{
			"00001_a.sql": "-- infraweave:compatible-from 0\n",
		})}, "not a version"},
		{"floor past the files", Stream{Name: "s", Files: fstest.MapFS{}, Floor: Floor{1, "v1.0.0"}}, "past the newest"},
		{"floor without a release", Stream{Name: "s", Files: mapFS(map[string]string{
			"00001_a.sql": "",
		}), Floor: Floor{Version: 1}}, "names no release"},
		{"unreadable file", Stream{Name: "s", Files: unreadable{mapFS(map[string]string{
			"00001_a.sql": "",
		})}}, "permission denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.s.Load()
			if !errors.Is(err, ErrStream) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want ErrStream naming %q", err, tc.want)
			}
		})
	}
	if _, err := loadAll([]Stream{good, good}); !errors.Is(err, ErrStream) ||
		!strings.Contains(err.Error(), "registered twice") {
		t.Errorf("a stream registered twice: %v", err)
	}
}

func TestUnitStatements(t *testing.T) {
	f := File{SQL: "-- infraweave:no-transaction\n-- a comment\n" +
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS a\n  ON t (x);\n\n" +
		"-- between\nDROP INDEX CONCURRENTLY IF EXISTS b;\nSELECT 1"}
	got := f.statements()
	want := []string{
		"-- infraweave:no-transaction\n-- a comment\nCREATE INDEX CONCURRENTLY IF NOT EXISTS a\n  ON t (x);",
		"-- between\nDROP INDEX CONCURRENTLY IF EXISTS b;",
		"SELECT 1",
	}
	if !slices.Equal(got, want) {
		t.Errorf("statements = %q", got)
	}
	if got := (File{SQL: "SELECT 1;\n-- trailing\n"}).statements(); !slices.Equal(
		got,
		[]string{"SELECT 1;"},
	) {
		t.Errorf("trailing comment: %q", got)
	}
}
