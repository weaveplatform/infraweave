package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// MaxSize bounds a plan document Parse reads, and a changes document Read
// reads. A plan's JSON grows with its resources' attributes, and a
// workspace of tens of thousands of resources stays well below it.
const MaxSize = 64 << 20

// Errors of a document Parse or Read refuses.
var (
	// ErrTooLarge is a document past MaxSize.
	ErrTooLarge = errors.New("plan: the document is larger than 64 MiB")
	// ErrMalformed is a document that is not the JSON its format names.
	ErrMalformed = errors.New("plan: the document is malformed")
	// ErrFormat is a document of a format version this package does not
	// read, or naming an action it does not know.
	ErrFormat = errors.New("plan: the document's format is not one this build reads")
)

// Type is what a change does to its resource, the reference's
// EntityChangeType.
//
// ref: report 08 §9
type Type string

// The types a Terraform or OpenTofu plan's changes take.
const (
	TypeAdd                        Type = "ADD"
	TypeChange                     Type = "CHANGE"
	TypeDelete                     Type = "DELETE"
	TypeReplaceDestroyBeforeCreate Type = "REPLACE_DESTROY_BEFORE_CREATE"
	TypeReplaceCreateBeforeDestroy Type = "REPLACE_CREATE_BEFORE_DESTROY"
	TypeNoop                       Type = "NOOP"
	TypeRead                       Type = "READ"
	TypeImport                     Type = "IMPORT"
	TypeForget                     Type = "FORGET"
	TypeCreateAfterForgotten       Type = "CREATE_AFTER_FORGOTTEN"
)

// Result is how far a change got, the reference's EntityChangeResult.
type Result string

// ResultPlanned is every change of a plan: none is applied yet.
const ResultPlanned Result = "PLANNED"

// Change is one resource's planned change, the reference's EntityChangeV3
// without its attribute changes.
//
// ref: report 08 §9
type Change struct {
	Address string `json:"address"`
	// PreviousAddress is the address a moved resource had, nil for one
	// that did not move.
	PreviousAddress *string  `json:"previousAddress,omitempty"`
	Metadata        Metadata `json:"metadata"`
}

// Metadata is a change's result, its type, and whether its resource moved.
type Metadata struct {
	Result Result `json:"result"`
	Type   Type   `json:"type"`
	Moved  bool   `json:"moved"`
}

// Delta counts a plan's changes to managed resources as Terraform's own
// summary does, a replacement once in Add and once in Delete: the
// resources it adds, changes in place, deletes, imports, moves and
// forgets, how many the state held before it, and whether any root
// module output changes. A data source is never counted.
type Delta struct {
	Add       int  `json:"add"`
	Change    int  `json:"change"`
	Delete    int  `json:"delete"`
	Import    int  `json:"import"`
	Move      int  `json:"move"`
	Forget    int  `json:"forget"`
	Resources int  `json:"resources"`
	Outputs   bool `json:"outputs"`
}

// Plan is a plan's changes, in the order the plan lists them, and its
// delta.
type Plan struct {
	Changes []Change
	Delta   Delta
}

// document is what Parse reads of `show -json` of a saved plan. Unknown
// members are left alone: a minor format version adds members, never
// changes these.
type document struct {
	FormatVersion   string                     `json:"format_version"`   //nolint:tagliatelle // Terraform's spelling
	PriorState      *priorState                `json:"prior_state"`      //nolint:tagliatelle // Terraform's spelling
	ResourceChanges []resourceChange           `json:"resource_changes"` //nolint:tagliatelle // Terraform's spelling
	OutputChanges   map[string]resourceActions `json:"output_changes"`   //nolint:tagliatelle // Terraform's spelling
}

type priorState struct {
	Values *struct {
		RootModule module `json:"root_module"` //nolint:tagliatelle // Terraform's spelling
	} `json:"values"`
}

type module struct {
	Resources []struct {
		Address string `json:"address"`
		Mode    string `json:"mode"`
	} `json:"resources"`
	ChildModules []module `json:"child_modules"` //nolint:tagliatelle // Terraform's spelling
}

type resourceChange struct {
	Address         string          `json:"address"`
	PreviousAddress string          `json:"previous_address"` //nolint:tagliatelle // Terraform's spelling
	Mode            string          `json:"mode"`
	Change          resourceActions `json:"change"`
}

type resourceActions struct {
	Actions []string `json:"actions"`
	// Importing is present, as an object, on a change that imports its
	// resource.
	Importing json.RawMessage `json:"importing"`
}

// counts are what one change adds to a delta's Add, Change, Delete and
// Forget.
type counts struct{ add, change, delete, forget int }

// actions maps each action list a plan names to its type and its counts.
var actions = map[string]struct {
	typ Type
	counts
}{
	"create":        {TypeAdd, counts{add: 1}},
	"update":        {TypeChange, counts{change: 1}},
	"delete":        {TypeDelete, counts{delete: 1}},
	"delete,create": {TypeReplaceDestroyBeforeCreate, counts{add: 1, delete: 1}},
	"create,delete": {TypeReplaceCreateBeforeDestroy, counts{add: 1, delete: 1}},
	"no-op":         {TypeNoop, counts{}},
	"read":          {TypeRead, counts{}},
	"forget":        {TypeForget, counts{forget: 1}},
	"create,forget": {TypeCreateAfterForgotten, counts{add: 1, forget: 1}},
}

// Parse reads r, the JSON of `terraform show -json` or `tofu show -json`
// of a saved plan, of format version 1.x, into its changes and delta. It
// refuses a document past MaxSize as ErrTooLarge, one that is not JSON
// of the format or has a change without an address as ErrMalformed, and
// another major format version or an action it does not know as
// ErrFormat.
func Parse(r io.Reader) (Plan, error) {
	raw, err := readAll(r)
	if err != nil {
		return Plan{}, err
	}
	var doc document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Plan{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if major, _, _ := strings.Cut(doc.FormatVersion, "."); major != "1" {
		return Plan{}, fmt.Errorf("%w: format_version %q", ErrFormat, doc.FormatVersion)
	}
	p := Plan{Changes: make([]Change, 0, len(doc.ResourceChanges))}
	imported := map[string]bool{}
	for i, rc := range doc.ResourceChanges {
		if rc.Address == "" {
			return Plan{}, fmt.Errorf("%w: resource change %d has no address", ErrMalformed, i)
		}
		a, ok := actions[strings.Join(rc.Change.Actions, ",")]
		if !ok {
			return Plan{}, fmt.Errorf("%w: %s has the actions %q", ErrFormat, rc.Address,
				rc.Change.Actions)
		}
		c := Change{Address: rc.Address, Metadata: Metadata{Result: ResultPlanned, Type: a.typ}}
		importing := len(rc.Change.Importing) > 0 && !bytes.Equal(rc.Change.Importing, null)
		if importing {
			c.Metadata.Type = TypeImport
		}
		moved := rc.PreviousAddress != "" && rc.PreviousAddress != rc.Address
		if moved {
			c.PreviousAddress, c.Metadata.Moved = &rc.PreviousAddress, true
		}
		p.Changes = append(p.Changes, c)
		if rc.Mode == "data" {
			continue
		}
		d := &p.Delta
		d.Add, d.Change = d.Add+a.add, d.Change+a.change
		d.Delete, d.Forget = d.Delete+a.delete, d.Forget+a.forget
		if importing {
			d.Import++
			imported[rc.Address] = true
		}
		if moved {
			d.Move++
		}
	}
	if doc.PriorState != nil && doc.PriorState.Values != nil {
		p.Delta.Resources = managed(doc.PriorState.Values.RootModule, imported)
	}
	for _, o := range doc.OutputChanges {
		if !slices.Equal(o.Actions, []string{"no-op"}) {
			p.Delta.Outputs = true
		}
	}
	return p, nil
}

// null is JSON's null, which an absent importing may also be.
var null = []byte("null")

// managed counts the managed resources of root and of every module below
// it but those the plan imports, which the tool reads into prior_state
// while it plans though the state holds them only once the plan applies.
// It walks the modules without recursion so a deep document cannot
// exhaust the stack.
func managed(root module, imported map[string]bool) int {
	n, todo := 0, []module{root}
	for len(todo) > 0 {
		m := todo[len(todo)-1]
		todo = append(todo[:len(todo)-1], m.ChildModules...)
		for _, r := range m.Resources {
			if r.Mode == "managed" && !imported[r.Address] {
				n++
			}
		}
	}
	return n
}

// readAll reads r to its end, refusing more than MaxSize bytes.
func readAll(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("plan: read the document: %w", err)
	}
	if len(raw) > MaxSize {
		return nil, ErrTooLarge
	}
	return raw, nil
}
