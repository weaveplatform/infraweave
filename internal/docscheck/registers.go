package docscheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	dataDir   = "docs/data"
	schemaDir = dataDir + "/schema"
	// schemaBase is where the compiler files each schema, so a $ref between
	// schemas resolves to another schema of this directory and never to the
	// network: .invalid is reserved and the compiler has no loader for it.
	schemaBase = "https://infraweave.invalid/"
)

// register is one lookup file under docs/data: a JSON object whose $schema
// names its schema in docs/data/schema and whose records each carry an id.
// The schema's x- keywords say how the rest of the tree refers to it:
//
//   - x-id-pattern: the anchored pattern every record id matches;
//   - x-cite-pattern: the pattern that finds a citation of one of its ids in
//     markdown and in other registers' text; each match must be an id;
//   - x-cited-in: a record property listing repository files, each of which
//     must cite the record's id, so a record can't name a document that has
//     forgotten it;
//   - x-evidence: a record property holding {file, present, absent} checks,
//     phrases that must and must not appear in that file, so a record that
//     says a document says something is held to it.
type register struct {
	path    string
	raw     string
	records []map[string]any
	ids     map[string]bool
	cite    *regexp.Regexp
	citedIn string
	proof   string
}

type evidence struct {
	File    string   `json:"file"`
	Present []string `json:"present"`
	Absent  []string `json:"absent"`
}

func (c *checker) registers() error {
	files, err := fs.Glob(c.repo, dataDir+"/*.json")
	if err != nil {
		return fmt.Errorf("list registers: %w", err)
	}
	schemas, err := fs.Glob(c.repo, schemaDir+"/*.schema.json")
	if err != nil {
		return fmt.Errorf("list schemas: %w", err)
	}
	comp := jsonschema.NewCompiler()
	for _, s := range schemas {
		doc, err := c.readJSON(s)
		if err != nil {
			c.add(s, 1, "not JSON: %v", err)
			continue
		}
		if err := comp.AddResource(schemaBase+s, doc); err != nil {
			return fmt.Errorf("add schema %s: %w", s, err)
		}
	}
	used := map[string]bool{}
	for _, f := range files {
		r := c.register(comp, f, used)
		if r != nil {
			c.regs = append(c.regs, r)
		}
	}
	for _, s := range schemas {
		if !used[s] && path.Base(s) != "common.schema.json" {
			c.add(s, 1, "no register in %s uses this schema", dataDir)
		}
	}
	for _, r := range c.regs {
		c.registerLinks(r)
		c.citedIn(r)
		c.evidence(r)
	}
	c.citations()
	return nil
}

func (c *checker) readJSON(p string) (any, error) {
	b, err := fs.ReadFile(c.repo, p)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", p, err)
	}
	return v, nil
}

func (c *checker) register(comp *jsonschema.Compiler, f string, used map[string]bool) *register {
	b, err := fs.ReadFile(c.repo, f)
	if err != nil {
		c.add(f, 1, "unreadable: %v", err)
		return nil
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		c.add(f, 1, "not JSON: %v", err)
		return nil
	}
	top, _ := inst.(map[string]any)
	ref, _ := top["$schema"].(string)
	want := "schema/" + strings.TrimSuffix(path.Base(f), ".json") + ".schema.json"
	if ref != want {
		c.add(f, 1, "$schema is %q, want %q", ref, want)
		return nil
	}
	s := path.Join(dataDir, ref)
	used[s] = true
	sch, err := comp.Compile(schemaBase + s)
	if err != nil {
		c.add(s, 1, "does not compile: %v", oneLine(err))
		return nil
	}
	if err := sch.Validate(inst); err != nil {
		c.add(f, 1, "does not match %s: %v", path.Base(s), oneLine(err))
		return nil
	}
	// The x- keywords are read from the schema as plain JSON: the compiler
	// ignores keywords it does not know.
	var meta map[string]any
	var doc struct {
		Records []map[string]any `json:"records"`
	}
	sb, _ := fs.ReadFile(c.repo, s)
	_ = json.Unmarshal(sb, &meta)
	keyword := func(k string) string { v, _ := meta[k].(string); return v }
	idPattern := keyword("x-id-pattern")
	if idPattern == "" {
		c.add(s, 1, "has no x-id-pattern")
		return nil
	}
	idPat, err := regexp.Compile("^(?:" + idPattern + ")$")
	if err != nil {
		c.add(s, 1, "x-id-pattern: %v", err)
		return nil
	}
	r := &register{
		path:    f,
		raw:     string(b),
		ids:     map[string]bool{},
		citedIn: keyword("x-cited-in"),
		proof:   keyword("x-evidence"),
	}
	if cite := keyword("x-cite-pattern"); cite != "" {
		if r.cite, err = regexp.Compile(cite); err != nil {
			c.add(s, 1, "x-cite-pattern: %v", err)
			return nil
		}
	}
	if err := json.Unmarshal(b, &doc); err != nil || doc.Records == nil {
		c.add(f, 1, "has no records array")
		return nil
	}
	r.records = doc.Records
	for _, rec := range doc.Records {
		id, _ := rec["id"].(string)
		switch {
		case !idPat.MatchString(id):
			c.add(f, r.line(id), "record id %q does not match x-id-pattern %s", id, idPattern)
		case r.ids[id]:
			c.add(f, r.line(id), "record id %s appears twice", id)
		}
		r.ids[id] = true
	}
	return r
}

// line is the line of the file that holds a record's id, so a finding points
// at the record rather than at the top of a file of hundreds.
func (r *register) line(id string) int {
	i := strings.Index(r.raw, `"id": "`+id+`"`)
	if i < 0 {
		return 1
	}
	return strings.Count(r.raw[:i], "\n") + 1
}

// stringsOf lists every string value of v, in document order.
func stringsOf(v any, out *[]string) {
	switch t := v.(type) {
	case string:
		*out = append(*out, t)
	case []any:
		for _, e := range t {
			stringsOf(e, out)
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			stringsOf(t[k], out)
		}
	}
}

// registerLinks checks the markdown links inside a register's text as if the
// text were a document in docs/data, which is where its relative links start.
func (c *checker) registerLinks(r *register) {
	d := &doc{path: r.path, anchors: map[string]bool{}}
	for _, rec := range r.records {
		var texts []string
		stringsOf(r.text(rec), &texts)
		id, _ := rec["id"].(string)
		for _, s := range texts {
			for _, m := range mdLink.FindAllStringSubmatch(codeSpan.ReplaceAllString(s, ""), -1) {
				c.link(d, r.line(id), m[1])
			}
		}
	}
}

// citations checks that every id a document or another register cites is
// a record of the register it belongs to.
func (c *checker) citations() {
	check := func(p string, line int, text string) {
		for _, r := range c.regs {
			if r.cite == nil {
				continue
			}
			for _, m := range r.cite.FindAllString(text, -1) {
				if !r.ids[m] {
					c.add(p, line, "cites %s, which %s does not hold", m, r.path)
				}
			}
		}
	}
	for _, d := range c.sorted() {
		for i, l := range d.lines {
			if !d.code[i] {
				check(d.path, i+1, l)
			}
		}
	}
	for _, r := range c.regs {
		for _, rec := range r.records {
			id, _ := rec["id"].(string)
			var texts []string
			stringsOf(r.text(rec), &texts)
			for _, s := range texts {
				check(r.path, r.line(id), s)
			}
		}
	}
}

// text is the part of a record that is the register's own prose. The id is
// a definition, not a citation, and evidence phrases are other files' text:
// their links start from those files, and an absent phrase holds exactly the
// words, an old ID among them, that must not exist any more.
func (r *register) text(rec map[string]any) map[string]any {
	out := make(map[string]any, len(rec))
	for k, v := range rec {
		if k != "id" && k != r.proof {
			out[k] = v
		}
	}
	return out
}

// mentions reports whether text names id as a whole token: C16 is not a
// mention of C1, and RES-02.06-16 is not one of RES-02.06-1.
func mentions(text, id string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], id)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(id)
		if (start == 0 || !idRune(text[start-1])) && (end == len(text) || !idRune(text[end])) {
			return true
		}
		i = start + 1
	}
}

func idRune(b byte) bool {
	return b == '-' || b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' ||
		b >= 'A' && b <= 'Z'
}

func (c *checker) citedIn(r *register) {
	if r.citedIn == "" {
		return
	}
	for _, rec := range r.records {
		id, _ := rec["id"].(string)
		var files []string
		switch v := rec[r.citedIn].(type) {
		case string:
			files = []string{v}
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok {
					files = append(files, s)
				}
			}
		}
		for _, f := range files {
			b, err := fs.ReadFile(c.repo, f)
			if err != nil {
				c.add(r.path, r.line(id), "%s names %s, which does not exist", id, f)
				continue
			}
			if !mentions(string(b), id) {
				c.add(r.path, r.line(id), "%s names %s, which does not cite it", id, f)
			}
		}
	}
}

var space = regexp.MustCompile(`\s+`)

func normalize(s string) string { return strings.TrimSpace(space.ReplaceAllString(s, " ")) }

// evidence checks each record's phrases against the file it names. Markdown
// wraps lines anywhere, so runs of whitespace compare as one space.
func (c *checker) evidence(r *register) {
	if r.proof == "" {
		return
	}
	for _, rec := range r.records {
		id, _ := rec["id"].(string)
		raw, ok := rec[r.proof]
		if !ok {
			continue
		}
		b, _ := json.Marshal(raw)
		var list []evidence
		if bytes.HasPrefix(bytes.TrimSpace(b), []byte("[")) {
			_ = json.Unmarshal(b, &list)
		} else {
			var one evidence
			_ = json.Unmarshal(b, &one)
			list = []evidence{one}
		}
		for _, e := range list {
			text, err := fs.ReadFile(c.repo, e.File)
			if err != nil {
				c.add(r.path, r.line(id), "%s: evidence file %s does not exist", id, e.File)
				continue
			}
			body := normalize(string(text))
			for _, p := range e.Present {
				if !strings.Contains(body, normalize(p)) {
					c.add(r.path, r.line(id), "%s: %s does not contain %q", id, e.File, p)
				}
			}
			for _, p := range e.Absent {
				if strings.Contains(body, normalize(p)) {
					c.add(r.path, r.line(id), "%s: %s still contains %q", id, e.File, p)
				}
			}
		}
	}
}

func oneLine(err error) string {
	return normalize(fmt.Sprint(err))
}
