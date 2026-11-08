package app

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
)

var (
	// alterTable matches a change to a table some earlier file created.
	// Until v0.1.0 ships no database needs upgrading, so a column, a
	// constraint or a key belongs in the CREATE TABLE that makes the table.
	alterTable = regexp.MustCompile(`(?i)\bALTER\s+TABLE\b`)
	// compatibleFrom is the header that refuses older binaries on a
	// schema, which has no older binary to refuse before the first release.
	compatibleFrom = regexp.MustCompile(`infraweave:compatible-from`)
	// citation matches a pointer into the project's records: a decision, a
	// report, a design-review or open-question ID, or a docs/ path. A
	// comment that leans on one reads as noise to anyone without it open,
	// so a citation goes on its own -- ref: line below a comment that
	// stands alone.
	citation = regexp.MustCompile(
		`(?i)\bdecisions?\s+\d{4}\b|\b\d{4}'s\b|\breports?\s+\d+\b|\b(DV|PD|OQ|DES)-\d|docs/`)
)

// migrationProblems lists each line of streams' files that alters a table,
// declares a compatible-from version, or cites a record outside a -- ref:
// line.
func migrationProblems(t *testing.T, streams []migrate.Stream) []string {
	t.Helper()
	var out []string
	for _, s := range streams {
		names, err := fs.Glob(s.Files, "*.sql")
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			data, err := fs.ReadFile(s.Files, name)
			if err != nil {
				t.Fatal(err)
			}
			sc := bufio.NewScanner(bytes.NewReader(data))
			for n := 1; sc.Scan(); n++ {
				line := sc.Text()
				at := fmt.Sprintf("%s %s:%d", s.Name, name, n)
				switch {
				case alterTable.MatchString(line):
					out = append(out, at+": ALTER TABLE; create the table in its final form")
				case compatibleFrom.MatchString(line):
					out = append(out, at+": a compatible-from header before the first release")
				case citation.MatchString(line) &&
					!strings.HasPrefix(strings.TrimSpace(line), "-- ref:"):
					out = append(out, at+": a citation outside a -- ref: line")
				}
			}
			if err := sc.Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	return out
}

// TestUnitMigrationsFinalForm checks that every registered stream creates
// its tables in their final form and keeps its citations on -- ref: lines.
func TestUnitMigrationsFinalForm(t *testing.T) {
	if got := migrationProblems(t, Streams()); len(got) != 0 {
		t.Errorf("migration problems:\n%s", strings.Join(got, "\n"))
	}
}

// TestUnitMigrationProblems checks what migrationProblems reports, one
// case per rule and one for what it allows.
func TestUnitMigrationProblems(t *testing.T) {
	stream := func(body string) []migrate.Stream {
		return []migrate.Stream{{Name: "s", Files: fstest.MapFS{
			"00001_a.sql": {Data: []byte(body)},
		}}}
	}
	for name, c := range map[string]struct {
		body string
		want []string
	}{
		"final form": {
			body: "-- A table that stands on its own.\n-- ref: decision 0004\n" +
				"CREATE TABLE a (id int);\n",
		},
		"alter": {
			body: "CREATE TABLE a (id int);\nalter table a ADD COLUMN b int;\n",
			want: []string{"s 00001_a.sql:2: ALTER TABLE; create the table in its final form"},
		},
		"compatible-from": {
			body: "-- infraweave:compatible-from 00001\nSELECT 1;\n",
			want: []string{"s 00001_a.sql:1: a compatible-from header before the first release"},
		},
		"citations": {
			body: "-- Decision 0007's tenancy.\n-- as 0021's 409 says\n-- report 28's users\n" +
				"-- DV-085\n-- see docs/implementation/plan.md\n    -- ref: decision 0006\n",
			want: []string{
				"s 00001_a.sql:1: a citation outside a -- ref: line",
				"s 00001_a.sql:2: a citation outside a -- ref: line",
				"s 00001_a.sql:3: a citation outside a -- ref: line",
				"s 00001_a.sql:4: a citation outside a -- ref: line",
				"s 00001_a.sql:5: a citation outside a -- ref: line",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := migrationProblems(t, stream(c.body)); !slices.Equal(got, c.want) {
				t.Errorf("problems = %q, want %q", got, c.want)
			}
		})
	}
}
