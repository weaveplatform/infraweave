package docscheck

import (
	"strings"
	"testing"
	"testing/fstest"
)

// noteSchema is a register schema with every x- keyword: notes are cited as
// N-<n>, name the reports that cite them, and carry evidence.
const noteSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "x-id-pattern": "N-[0-9]+",
  "x-cite-pattern": "\\bN-[0-9]+\\b",
  "x-cited-in": "reports",
  "x-evidence": "evidence",
  "type": "object",
  "required": ["$schema", "records"],
  "properties": {
    "records": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["id", "text"],
        "properties": {
          "id": {"type": "string"},
          "text": {"$ref": "common.schema.json#/$defs/markdown"},
          "reports": {"anyOf": [{"$ref": "common.schema.json#/$defs/path"}, {"type": "array", "items": {"$ref": "common.schema.json#/$defs/path"}}]},
          "evidence": {}
        }
      }
    }
  }
}`

const commonSchema = `{"$defs": {"markdown": {"type": "string", "minLength": 1}, "path": {"type": "string"}}}`

const notes = `{
  "$schema": "schema/notes.schema.json",
  "records": [
    {"id": "N-1", "text": "See [01](../research/reference/01-a.md#01-a-report) and N-2.",
     "reports": ["docs/research/reference/01-a.md"],
     "evidence": {"file": "docs/research/reference/01-a.md", "present": ["Checked against:   v1"], "absent": ["v2", "N-9, see [gone](gone.md)"]}},
    {"id": "N-2", "text": "A second note.",
     "evidence": [{"file": "docs/research/reference/01-a.md", "present": ["A report"]}]}
  ]
}`

// registerTree is tree() with one register whose report cites N-1. N-1's
// absent phrase cites a missing id and links a missing file, which the
// checks leave alone: evidence is another file's text.
func registerTree() fstest.MapFS {
	m := tree()
	m["docs/data/schema/common.schema.json"] = file(commonSchema)
	m["docs/data/schema/notes.schema.json"] = file(noteSchema)
	m["docs/data/notes.json"] = file(notes)
	m["docs/research/reference/01-a.md"] = file(
		goodReport + "\nN-1 is open; `N-1` too.\n\n```\nN-9 in code is not a citation\n```\n",
	)
	return m
}

func TestUnitRegistersPass(t *testing.T) {
	found, err := Check(registerTree())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Error(f)
	}
}

func TestUnitRegisterFindings(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(fstest.MapFS)
		want  string
		where string
	}{
		{
			name:  "not JSON",
			edit:  func(m fstest.MapFS) { m["docs/data/notes.json"] = file("{") },
			want:  "not JSON",
			where: "docs/data/notes.json:1",
		},
		{
			name:  "schema not JSON",
			edit:  func(m fstest.MapFS) { m["docs/data/schema/notes.schema.json"] = file("{") },
			want:  "not JSON",
			where: "docs/data/schema/notes.schema.json:1",
		},
		{
			name: "wrong $schema",
			edit: func(m fstest.MapFS) {
				m["docs/data/notes.json"] = file(
					strings.Replace(notes, "schema/notes.schema.json", "schema/x.schema.json", 1),
				)
			},
			want:  `$schema is "schema/x.schema.json", want "schema/notes.schema.json"`,
			where: "docs/data/notes.json:1",
		},
		{
			name: "schema that does not compile",
			edit: func(m fstest.MapFS) {
				m["docs/data/schema/notes.schema.json"] = file(`{"type": 5}`)
			},
			want:  "does not compile",
			where: "docs/data/schema/notes.schema.json:1",
		},
		{
			name: "record against the schema",
			edit: func(m fstest.MapFS) {
				m["docs/data/notes.json"] = file(
					strings.Replace(notes, `"text": "A second note.",`, "", 1),
				)
			},
			want:  "does not match notes.schema.json",
			where: "docs/data/notes.json:1",
		},
		{
			name: "schema without an id pattern",
			edit: func(m fstest.MapFS) {
				m["docs/data/schema/notes.schema.json"] = file(
					strings.Replace(noteSchema, `"x-id-pattern": "N-[0-9]+",`, "", 1),
				)
			},
			want:  "has no x-id-pattern",
			where: "docs/data/schema/notes.schema.json:1",
		},
		{
			name: "bad id pattern",
			edit: func(m fstest.MapFS) {
				m["docs/data/schema/notes.schema.json"] = file(
					strings.Replace(noteSchema, `"N-[0-9]+",`, `"N-[",`, 1),
				)
			},
			want:  "x-id-pattern:",
			where: "docs/data/schema/notes.schema.json:1",
		},
		{
			name: "bad cite pattern",
			edit: func(m fstest.MapFS) {
				m["docs/data/schema/notes.schema.json"] = file(
					strings.Replace(noteSchema, `"\\bN-[0-9]+\\b"`, `"("`, 1),
				)
			},
			want:  "x-cite-pattern:",
			where: "docs/data/schema/notes.schema.json:1",
		},
		{
			name: "no records",
			edit: func(m fstest.MapFS) {
				m["docs/data/schema/notes.schema.json"] = file(`{"x-id-pattern": "N-[0-9]+"}`)
				m["docs/data/notes.json"] = file(`{"$schema": "schema/notes.schema.json"}`)
			},
			want:  "has no records array",
			where: "docs/data/notes.json:1",
		},
		{
			name: "id against the pattern",
			edit: func(m fstest.MapFS) {
				m["docs/data/notes.json"] = file(
					strings.Replace(notes, `"id": "N-2"`, `"id": "X-2"`, 1),
				)
			},
			want:  `record id "X-2" does not match`,
			where: "docs/data/notes.json:7",
		},
		{
			name: "duplicate id",
			edit: func(m fstest.MapFS) {
				m["docs/data/notes.json"] = file(
					strings.Replace(notes, `"id": "N-2"`, `"id": "N-1"`, 1),
				)
				m["docs/research/reference/01-a.md"] = file(goodReport + "\nN-1\n")
			},
			want:  "record id N-1 appears twice",
			where: "docs/data/notes.json:4",
		},
		{
			name: "broken link in a record",
			edit: func(m fstest.MapFS) {
				m["docs/data/notes.json"] = file(
					strings.Replace(notes, "01-a.md#01-a-report", "01-a.md#nope", 1),
				)
			},
			want:  "has no heading #nope",
			where: "docs/data/notes.json:4",
		},
		{
			name: "document cites a missing id",
			edit: func(m fstest.MapFS) {
				m["docs/a.md"] = file("# A\n\nN-7 is cited.\n")
			},
			want:  "cites N-7, which docs/data/notes.json does not hold",
			where: "docs/a.md:3",
		},
		{
			name: "register cites a missing id",
			edit: func(m fstest.MapFS) {
				m["docs/data/notes.json"] = file(strings.Replace(notes, "and N-2.", "and N-3.", 1))
			},
			want:  "cites N-3",
			where: "docs/data/notes.json:4",
		},
		{
			name: "report that does not cite its record",
			edit: func(m fstest.MapFS) {
				m["docs/research/reference/01-a.md"] = file(
					goodReport + "\nN-10 is a different note.\n",
				)
				m["docs/data/notes.json"] = file(
					strings.Replace(
						notes,
						`"text": "A second note."`,
						`"text": "A second note.", "reports": []`,
						1,
					) +
						"",
				)
			},
			want:  "N-1 names docs/research/reference/01-a.md, which does not cite it",
			where: "docs/data/notes.json:4",
		},
		{
			name: "record names a missing report",
			edit: func(m fstest.MapFS) {
				m["docs/data/notes.json"] = file(
					strings.Replace(
						notes,
						`"reports": ["docs/research/reference/01-a.md"]`,
						`"reports": "docs/gone.md"`,
						1,
					),
				)
			},
			want:  "N-1 names docs/gone.md, which does not exist",
			where: "docs/data/notes.json:4",
		},
		{
			name: "missing evidence phrase",
			edit: func(m fstest.MapFS) {
				m["docs/data/notes.json"] = file(
					strings.Replace(notes, `"A report"`, `"Another report"`, 1),
				)
			},
			want:  `N-2: docs/research/reference/01-a.md does not contain "Another report"`,
			where: "docs/data/notes.json:7",
		},
		{
			name: "phrase that should be gone",
			edit: func(m fstest.MapFS) {
				m["docs/data/notes.json"] = file(
					strings.Replace(notes, `"absent": ["v2"`, `"absent": ["v1"`, 1),
				)
			},
			want:  `N-1: docs/research/reference/01-a.md still contains "v1"`,
			where: "docs/data/notes.json:4",
		},
		{
			name: "evidence file missing",
			edit: func(m fstest.MapFS) {
				m["docs/data/notes.json"] = file(
					strings.Replace(
						notes,
						`[{"file": "docs/research/reference/01-a.md"`,
						`[{"file": "docs/gone.md"`,
						1,
					),
				)
			},
			want:  "N-2: evidence file docs/gone.md does not exist",
			where: "docs/data/notes.json:7",
		},
		{
			name:  "schema no register uses",
			edit:  func(m fstest.MapFS) { m["docs/data/schema/spare.schema.json"] = file(`{}`) },
			want:  "no register in docs/data uses this schema",
			where: "docs/data/schema/spare.schema.json:1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := registerTree()
			tc.edit(m)
			found, err := Check(m)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range found {
				if strings.Contains(f.Message, tc.want) &&
					strings.HasPrefix(f.String(), tc.where+":") {
					return
				}
			}
			t.Errorf("want %s: ...%s..., got %q", tc.where, tc.want, found)
		})
	}
}

func TestUnitMentions(t *testing.T) {
	for _, tc := range []struct {
		text, id string
		want     bool
	}{
		{"see C16.", "C16", true},
		{"see C160", "C16", false},
		{"XC16", "C16", false},
		{"RES-02.06-16 and RES-02.06-1", "RES-02.06-1", true},
		{"RES-02.06-16", "RES-02.06-1", false},
		{"", "C1", false},
	} {
		if got := mentions(tc.text, tc.id); got != tc.want {
			t.Errorf("mentions(%q, %q) = %v", tc.text, tc.id, got)
		}
	}
}
