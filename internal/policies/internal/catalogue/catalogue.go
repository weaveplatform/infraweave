package catalogue

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/open-policy-agent/opa/v1/ast"
	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

// Revision is updated only by an explicit source/inventory/corpus refresh.
const Revision = "8e8894fbd4cefffa3c261746bcdd70ef9fbb53a8"

// ErrMissing reports a template absent from the supported, served catalogue.
var ErrMissing = errors.New("policy template not found")

// ErrPin refuses an incomplete or inconsistent embedded source inventory.
var ErrPin = errors.New("invalid policy catalogue pin")

//go:embed upstream
var upstream embed.FS

// Template preserves the source metadata and captured stable public identity.
// Engine is the dialect the adapted body parses under; it is explicit for old
// V0 examples rather than relying on policy creation's intentional V1 default.
type Template struct {
	ID, Name, Slug, Description, Body string
	Type                              rego.Type
	Engine                            rego.EngineType
	Labels                            []string
	Source                            string
	Rewrites                          int
}

var library = sync.OnceValues(func() ([]Template, error) { return load(upstream) })

// All returns independent metadata slices, so consumers cannot mutate the pin.
func All() ([]Template, error) {
	rows, err := library()
	if err != nil {
		return nil, err
	}
	out := slices.Clone(rows)
	for i := range out {
		out[i].Labels = slices.Clone(out[i].Labels)
	}
	return out, nil
}

// Get accepts the captured opaque ID, never a source file path.
func Get(id string) (Template, error) {
	rows, err := All()
	if err != nil {
		return Template{}, err
	}
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return Template{}, ErrMissing
}

func load(source fs.FS) ([]Template, error) {
	var identities []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
		Type string `json:"type"`
	}
	raw, err := fs.ReadFile(source, "upstream/CATALOGUE.json")
	if err != nil {
		return nil, fmt.Errorf("catalogue: read identities: %w", err)
	}
	if err := json.Unmarshal(raw, &identities); err != nil {
		return nil, fmt.Errorf("catalogue: decode identities: %w", err)
	}
	byName := map[string]int{}
	for i, row := range identities {
		byName[row.Type+":"+row.Name] = i
	}
	files, err := fs.Glob(source, "upstream/examples/*/*.yml")
	if err != nil {
		return nil, fmt.Errorf("catalogue: discover templates: %w", err)
	}
	rows := make([]Template, 0, len(identities))
	for _, file := range files {
		var meta struct {
			Name        string   `yaml:"name"`
			Source      string   `yaml:"source"`
			Type        string   `yaml:"type"`
			Description string   `yaml:"description"`
			Labels      []string `yaml:"labels"`
		}
		raw, err := fs.ReadFile(source, file)
		if err != nil {
			return nil, fmt.Errorf("catalogue: read %s: %w", file, err)
		}
		decoder := yaml.NewDecoder(bytes.NewReader(raw))
		decoder.KnownFields(true)
		if err := decoder.Decode(&meta); err != nil {
			return nil, fmt.Errorf("catalogue: decode %s: %w", file, err)
		}
		kind := strings.ToUpper(meta.Type)
		if kind == "LOGIN" {
			continue
		}
		if kind == "PUSH" {
			kind = "GIT_PUSH"
		}
		identity, ok := byName[kind+":"+meta.Name]
		if !ok || path.Base(meta.Source) != meta.Source || !strings.HasSuffix(meta.Source, ".rego") {
			return nil, fmt.Errorf("%w: invalid template identity in %s", ErrPin, file)
		}
		origin := path.Join(path.Dir(file), meta.Source)
		raw, err = fs.ReadFile(source, origin)
		if err != nil {
			return nil, fmt.Errorf("catalogue: read %s: %w", origin, err)
		}
		body, changes, err := Rewrite(origin, string(raw))
		if err != nil {
			return nil, err
		}
		engine := rego.V1
		if _, err := ast.ParseModuleWithOpts(origin, body, ast.ParserOptions{RegoVersion: ast.RegoV1}); err != nil {
			engine = rego.V0
		}
		id := identities[identity]
		rows = append(
			rows,
			Template{
				ID:          id.ID,
				Name:        meta.Name,
				Slug:        id.Slug,
				Description: meta.Description,
				Body:        body,
				Type:        rego.Type(kind),
				Engine:      engine,
				Labels:      meta.Labels,
				Source:      strings.TrimPrefix(origin, "upstream/"),
				Rewrites:    changes,
			},
		)
		delete(byName, kind+":"+meta.Name)
	}
	if len(files) != 68 || len(rows) != 62 || len(identities) != 62 || len(byName) != 0 {
		return nil, fmt.Errorf("%w: %d sources, %d served, %d unmatched", ErrPin, len(files), len(rows), len(byName))
	}
	slices.SortFunc(rows, func(a, b Template) int { return strings.Compare(a.Slug, b.Slug) })
	return rows, nil
}
