package specref

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

// brunoRoot is the collection directory inside spacelift-api-bruno.
const brunoRoot = "Spacelift/"

// notesMarker separates the schema-synced part of a docs block from the
// hand-written notes; only the synced part describes the API itself.
const notesMarker = "<!-- notes"

var (
	ErrBruUnterminated = errors.New("unterminated .bru block")
	ErrNoOperation     = errors.New("graphql body has no operation")
)

// BrunoOperation is one request from the collection, reduced to what the
// reference needs. The committed inventory holds these, not the .bru files.
type BrunoOperation struct {
	Path       string          `json:"path"`
	Folder     string          `json:"folder"`
	Subfolder  string          `json:"subfolder,omitempty"`
	Name       string          `json:"name"`
	Operation  string          `json:"operation"`
	RootFields []string        `json:"rootFields"`
	Docs       string          `json:"docs,omitempty"`
	Variables  json.RawMessage `json:"variables,omitempty"`
}

// BrunoFolder is a collection folder; its docs describe the API area and
// become the OpenAPI tag description.
type BrunoFolder struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Docs string `json:"docs,omitempty"`
}

// BrunoInventory is the committed extract of the collection.
type BrunoInventory struct {
	Folders  []BrunoFolder    `json:"folders"`
	Requests []BrunoOperation `json:"requests"`
}

// parseBru splits a .bru file into its top-level blocks. Block bodies are
// indented two spaces; the indentation is removed.
func parseBru(content []byte) (map[string]string, error) {
	blocks := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		name, ok := strings.CutSuffix(line, " {")
		if !ok || name == "" || strings.HasPrefix(name, " ") {
			continue
		}
		var body []string
		closed := false
		for i++; i < len(lines); i++ {
			if lines[i] == "}" {
				closed = true
				break
			}
			body = append(body, strings.TrimPrefix(lines[i], "  "))
		}
		if !closed {
			return nil, fmt.Errorf("%w: %s", ErrBruUnterminated, name)
		}
		blocks[name] = strings.TrimSpace(strings.Join(body, "\n"))
	}
	return blocks, nil
}

// metaName reads the name entry of a meta block.
func metaName(meta string) string {
	for line := range strings.SplitSeq(meta, "\n") {
		if v, ok := strings.CutPrefix(line, "name:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ParseBrunoRequest reads one request file. rel is its path inside the
// collection directory, e.g. "Stacks/Create Stack.bru".
func ParseBrunoRequest(rel string, content []byte) (*BrunoOperation, error) {
	blocks, err := parseBru(content)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	body, ok := blocks["body:graphql"]
	if !ok {
		return nil, nil //nolint:nilnil // a request without a GraphQL body is not an operation
	}
	op, roots, err := graphqlRootFields(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	dirs := strings.Split(path.Dir(rel), "/")
	b := &BrunoOperation{
		Path:       rel,
		Folder:     dirs[0],
		Name:       metaName(blocks["meta"]),
		Operation:  op,
		RootFields: roots,
	}
	if len(dirs) > 1 {
		b.Subfolder = dirs[1]
	}
	if docs, _, _ := strings.Cut(blocks["docs"], notesMarker); docs != "" {
		b.Docs = strings.TrimSpace(docs)
	}
	if vars := blocks["body:graphql:vars"]; vars != "" {
		var buf bytes.Buffer
		// Variables that are not valid JSON (a template placeholder left
		// unquoted) are dropped: they cannot serve as an example.
		if json.Compact(&buf, []byte(vars)) == nil {
			b.Variables = buf.Bytes()
		}
	}
	return b, nil
}

// ReadBrunoTarball extracts every folder and request in the collection,
// sorted by path.
func ReadBrunoTarball(tgz []byte) (*BrunoInventory, error) {
	inv := &BrunoInventory{}
	err := walkTarball(tgz, func(p string, content []byte) error {
		rel, ok := strings.CutPrefix(p, brunoRoot)
		if !ok || !strings.HasSuffix(rel, ".bru") || rel == "collection.bru" ||
			strings.HasPrefix(rel, "environments/") {
			return nil
		}
		if path.Base(rel) == "folder.bru" {
			blocks, err := parseBru(content)
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			docs, _, _ := strings.Cut(blocks["docs"], notesMarker)
			inv.Folders = append(
				inv.Folders,
				BrunoFolder{
					Path: path.Dir(rel),
					Name: metaName(blocks["meta"]),
					Docs: strings.TrimSpace(docs),
				},
			)
			return nil
		}
		op, err := ParseBrunoRequest(rel, content)
		if err != nil || op == nil {
			return err
		}
		inv.Requests = append(inv.Requests, *op)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(
		inv.Folders,
		func(i, j int) bool { return inv.Folders[i].Path < inv.Folders[j].Path },
	)
	sort.Slice(
		inv.Requests,
		func(i, j int) bool { return inv.Requests[i].Path < inv.Requests[j].Path },
	)
	return inv, nil
}

// graphqlRootFields returns the operation type and the root fields selected
// by the first operation in a GraphQL document. Only the outermost selection
// set matters, so this is a bracket-aware scan, not a GraphQL parser.
func graphqlRootFields(doc string) (string, []string, error) {
	toks := tokenizeGraphQL(doc)
	i := 0
	op := "query"
	if i < len(toks) && (toks[i] == "query" || toks[i] == "mutation" || toks[i] == "subscription") {
		op = toks[i]
		i++
	}
	// Skip the operation name, variable definitions and directives.
	for i < len(toks) && toks[i] != "{" {
		if toks[i] == "(" {
			i = skipBalanced(toks, i, "(", ")")
			continue
		}
		i++
	}
	if i >= len(toks) {
		return "", nil, ErrNoOperation
	}
	i++ // past "{"
	var roots []string
	for i < len(toks) && toks[i] != "}" {
		switch {
		case toks[i] == "(":
			i = skipBalanced(toks, i, "(", ")")
		case toks[i] == "{":
			i = skipBalanced(toks, i, "{", "}")
		case toks[i] == "...":
			i += 2 // fragment spread or inline fragment keyword
		case i+1 < len(toks) && toks[i+1] == ":":
			i += 2 // alias; the field name follows
		case toks[i] == "@":
			i += 2
		default:
			roots = append(roots, toks[i])
			i++
		}
	}
	if len(roots) == 0 {
		return "", nil, ErrNoOperation
	}
	return op, roots, nil
}

// skipBalanced returns the index after the bracket that closes toks[i].
func skipBalanced(toks []string, i int, open, closing string) int {
	depth := 0
	for ; i < len(toks); i++ {
		switch toks[i] {
		case open:
			depth++
		case closing:
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return i
}

// tokenizeGraphQL splits a document into names, punctuation and string
// literals, dropping comments and commas (insignificant in GraphQL).
func tokenizeGraphQL(doc string) []string {
	var toks []string
	for i := 0; i < len(doc); {
		c := doc[i]
		switch {
		case c == '#':
			for i < len(doc) && doc[i] != '\n' {
				i++
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
			i++
		case c == '"':
			j := i + 1
			for j < len(doc) && doc[j] != '"' {
				if doc[j] == '\\' {
					j++
				}
				j++
			}
			toks = append(toks, `""`)
			i = j + 1
		case strings.HasPrefix(doc[i:], "..."):
			toks = append(toks, "...")
			i += 3
		case isNameByte(c):
			j := i
			for j < len(doc) && isNameByte(doc[j]) {
				j++
			}
			toks = append(toks, doc[i:j])
			i = j
		default:
			toks = append(toks, string(c))
			i++
		}
	}
	return toks
}

func isNameByte(c byte) bool {
	return c == '_' || c == '$' || c == '-' || c == '.' || (c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z')
}
